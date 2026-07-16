package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/gitutil"
)

func TestMergeSelectors(t *testing.T) {
	cases := []struct {
		only, args, want []string
	}{
		{nil, nil, []string{}},
		{nil, []string{"studio"}, []string{"studio"}},
		{[]string{"service"}, nil, []string{"service"}},
		{[]string{"service"}, []string{"studio", "mobile"}, []string{"service", "studio", "mobile"}},
	}
	for _, c := range cases {
		got := mergeSelectors(c.only, c.args)
		if !equalStrings(got, c.want) {
			t.Errorf("mergeSelectors(%v, %v) = %v, want %v", c.only, c.args, got, c.want)
		}
	}
}

// initSourceRepo creates a local git repo with one commit and returns its path,
// so a pull test never touches the network (a local path is a valid clone URL).
func initSourceRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
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
	git("-c", "init.defaultBranch=main", "init")
	mustWrite(t, filepath.Join(dir, "README.md"), "hi\n")
	git("add", ".")
	git("commit", "-m", "initial")
	return dir
}

// TestRunPullUpdatesClonedRepos points pull at an explicit workspace that has a
// clone and verifies it succeeds against the configured repo.
func TestRunPullUpdatesClonedRepos(t *testing.T) {
	src := initSourceRepo(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	mustWrite(t, filepath.Join(dir, "tarjan.yaml"),
		"name: demo\nworkspaceRoot: "+root+"\nrepos:\n  - name: app\n    url: "+src+"\n")
	t.Chdir(dir)
	setConfigFlag(t, "")

	// A workspace with the repo already cloned into it.
	ws := t.TempDir()
	if err := gitutil.CloneAll(context.Background(), ws, []config.Repo{{Name: "app", URL: src}}); err != nil {
		t.Fatalf("clone setup: %v", err)
	}

	prev := pullWorkspace
	pullWorkspace = ws
	t.Cleanup(func() { pullWorkspace = prev })

	if err := runPull(pullCmd, nil); err != nil {
		t.Fatalf("runPull = %v, want nil", err)
	}
}

// TestRunPullUnknownVersionErrors verifies a version with no materialised
// workspace fails clearly instead of pulling nothing silently.
func TestRunPullUnknownVersionErrors(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	mustWrite(t, filepath.Join(dir, "tarjan.yaml"),
		"name: demo\nworkspaceRoot: "+root+"\nrepos:\n  - name: app\n    url: https://example.com/app.git\n")
	t.Chdir(dir)
	setConfigFlag(t, "")

	prev := pullWorkspace
	pullWorkspace = ""
	t.Cleanup(func() { pullWorkspace = prev })

	err := runPull(pullCmd, []string{"nope"})
	if err == nil {
		t.Fatal("runPull with an unknown version = nil, want error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error does not name the missing version: %v", err)
	}
}

// TestRunPullNoReposIsNoop verifies a config with no repos is a clean no-op.
func TestRunPullNoReposIsNoop(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := pullWorkspace
	pullWorkspace = ws
	t.Cleanup(func() { pullWorkspace = prev })

	if err := runPull(pullCmd, nil); err != nil {
		t.Fatalf("runPull(no repos) = %v, want nil", err)
	}
}
