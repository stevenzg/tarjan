package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

// initSourceRepo creates a local git repository with a single commit on the
// given branch and returns its path. A local path is a valid git clone URL, so
// tests never touch the network.
func initSourceRepo(t *testing.T, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("-c", "init.defaultBranch="+branch, "init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

func headBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCloneAll(t *testing.T) {
	src := initSourceRepo(t, "main")
	ws := t.TempDir()
	repos := []config.Repo{{Name: "app", URL: src}}

	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("CloneAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "app", "README.md")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
	if !isGitRepo(filepath.Join(ws, "app")) {
		t.Fatal("clone target is not a git repo")
	}
}

// TestCloneAllSkipsExisting verifies an already-cloned checkout is left
// untouched on a second run (workspace reuse), not wiped and re-cloned.
func TestCloneAllSkipsExisting(t *testing.T) {
	src := initSourceRepo(t, "main")
	ws := t.TempDir()
	repos := []config.Repo{{Name: "app", URL: src}}

	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("first CloneAll: %v", err)
	}
	sentinel := filepath.Join(ws, "app", "SENTINEL")
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("second CloneAll: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("existing checkout was re-cloned (sentinel disappeared)")
	}
}

// TestIsGitRepoAcceptsGitFile checks a checkout whose ".git" is a file (a linked
// worktree or submodule) is recognised as a repo, so it isn't mistaken for
// "not cloned" and re-cloned into a non-empty directory.
func TestIsGitRepoAcceptsGitFile(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Fatal("empty dir should not be a git repo")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isGitRepo(dir) {
		t.Fatal("a .git file (worktree/submodule) should count as a git repo")
	}
}

func TestCloneAllBranch(t *testing.T) {
	src := initSourceRepo(t, "dev")
	ws := t.TempDir()
	repos := []config.Repo{{Name: "app", URL: src, Branch: "dev"}}

	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("CloneAll: %v", err)
	}
	if got := headBranch(t, filepath.Join(ws, "app")); got != "dev" {
		t.Fatalf("checked-out branch = %q, want dev", got)
	}
}

func TestCloneAllDirOverride(t *testing.T) {
	src := initSourceRepo(t, "main")
	ws := t.TempDir()
	repos := []config.Repo{{Name: "app", URL: src, Dir: filepath.Join("nested", "checkout")}}

	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("CloneAll: %v", err)
	}
	if !isGitRepo(filepath.Join(ws, "nested", "checkout")) {
		t.Fatal("repo was not cloned into its Dir override path")
	}
}

// TestCloneArgsHardened verifies the clone invocation blocks git's ext::
// transport and terminates option parsing with "--", so a hostile config URL or
// branch cannot inject a git flag or run an arbitrary command.
func TestCloneArgsHardened(t *testing.T) {
	args := cloneArgs(config.Repo{URL: "ext::sh -c 'id'", Branch: "-x"}, "/ws/app")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "protocol.ext.allow=never") {
		t.Errorf("clone args do not disable the ext transport: %v", args)
	}

	// The URL and dest must appear after a "--" terminator.
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		t.Fatalf("clone args have no -- terminator: %v", args)
	}
	rest := args[sep+1:]
	if len(rest) != 2 || rest[0] != "ext::sh -c 'id'" || rest[1] != "/ws/app" {
		t.Errorf("URL/dest not positioned as positional args after --: %v", rest)
	}
	// The branch value is the argument to --branch, never a standalone token.
	for i, a := range args {
		if a == "-x" && (i == 0 || args[i-1] != "--branch") {
			t.Errorf("branch %q appears as a bare token, not a --branch value: %v", a, args)
		}
	}
}

// TestCloneRejectsExtTransport is an end-to-end check that a malicious ext::
// URL does not execute its command (it must fail to clone, not run `touch`).
func TestCloneRejectsExtTransport(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	repos := []config.Repo{{Name: "evil", URL: "ext::sh -c 'touch " + marker + "'"}}

	if err := CloneAll(context.Background(), ws, repos); err == nil {
		t.Fatal("clone of an ext:: URL succeeded, want failure")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ext:: transport executed its command — RCE guard is not effective")
	}
}

func TestCloneAllErrorWrapsRepoName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	repos := []config.Repo{{Name: "broken", URL: filepath.Join(t.TempDir(), "does-not-exist")}}

	err := CloneAll(context.Background(), ws, repos)
	if err == nil {
		t.Fatal("CloneAll on a bad URL = nil error, want error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error does not name the failing repo: %v", err)
	}
}

// gitIn runs a git command in dir with a deterministic author/committer, so
// commits made during a test don't depend on the ambient git config.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestPullAllFastForwards clones a repo, adds a commit upstream, then PullAll
// must fast-forward the workspace checkout to include it.
func TestPullAllFastForwards(t *testing.T) {
	src := initSourceRepo(t, "main")
	ws := t.TempDir()
	repos := []config.Repo{{Name: "app", URL: src}}

	if err := CloneAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("CloneAll: %v", err)
	}
	// A new commit lands upstream after the clone.
	if err := os.WriteFile(filepath.Join(src, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, src, "add", ".")
	gitIn(t, src, "commit", "-m", "second")

	dest := filepath.Join(ws, "app")
	if _, err := os.Stat(filepath.Join(dest, "NEW.md")); err == nil {
		t.Fatal("new file present before pull — clone unexpectedly current")
	}
	if err := PullAll(context.Background(), ws, repos); err != nil {
		t.Fatalf("PullAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "NEW.md")); err != nil {
		t.Fatalf("pull did not bring the upstream commit down: %v", err)
	}
}

// TestPullAllReportsUncloned verifies a repo that hasn't been cloned into the
// workspace is reported (named in the error) and skipped, not treated as OK.
func TestPullAllReportsUncloned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	repos := []config.Repo{{Name: "missing", URL: "https://example.com/missing.git"}}

	err := PullAll(context.Background(), ws, repos)
	if err == nil {
		t.Fatal("PullAll over an uncloned repo = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error does not name the uncloned repo: %v", err)
	}
}
