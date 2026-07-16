// Package gitutil clones and updates the repositories that make up an
// environment, shelling out to the system git so existing credentials and SSH
// config just work.
package gitutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/ui"
)

// cloneConcurrency bounds how many repos clone at once. Clones are independent
// and network-bound, so running a few in parallel cuts `up` startup markedly
// while staying polite to the git host.
const cloneConcurrency = 4

// CloneAll clones every repo into the workspace, up to cloneConcurrency at a
// time. If a repo directory already exists it is left untouched (a fresh `up`
// uses a fresh workspace, so this only matters when reusing a workspace). Each
// clone's git output is captured and printed on completion (rather than
// streamed) so parallel clones don't interleave into unreadable output.
func CloneAll(ctx context.Context, workspace string, repos []config.Repo) error {
	var pending []config.Repo
	for _, r := range repos {
		if isGitRepo(filepath.Join(workspace, r.Path())) {
			ui.Step("%s already cloned", r.Name)
			continue
		}
		pending = append(pending, r)
	}
	if len(pending) == 0 {
		return nil
	}

	sem := make(chan struct{}, cloneConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, r := range pending {
		wg.Add(1)
		go func(r config.Repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dest := filepath.Join(workspace, r.Path())
			out, err := clone(ctx, r, dest)
			mu.Lock()
			defer mu.Unlock()
			if len(out) > 0 {
				_, _ = os.Stderr.Write(out)
			}
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("clone %s: %w", r.Name, err)
				}
				ui.Warn("clone %s failed: %v", r.Name, err)
				return
			}
			ui.Step("cloned %s", r.Name)
		}(r)
	}
	wg.Wait()
	return firstErr
}

// PullAll updates every already-cloned repo in the workspace with `git pull
// --ff-only`, so it advances a checkout to its upstream but never rewrites or
// merges local history — a repo that can't fast-forward (or isn't cloned yet)
// is reported and skipped rather than aborting the rest. It tries every repo
// and returns a single error naming the ones that could not be updated.
func PullAll(ctx context.Context, workspace string, repos []config.Repo) error {
	var failed []string
	for _, r := range repos {
		dest := filepath.Join(workspace, r.Path())
		if !isGitRepo(dest) {
			ui.Warn("%s not cloned yet — run `tarjan up` first", r.Name)
			failed = append(failed, r.Name)
			continue
		}
		if err := pull(ctx, dest); err != nil {
			ui.Warn("%s: %v", r.Name, err)
			failed = append(failed, r.Name)
			continue
		}
		ui.Step("pulled %s", r.Name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not update: %s", strings.Join(failed, ", "))
	}
	return nil
}

// pull fast-forwards the checkout at dest to its upstream tracking branch. It
// uses `-C dest` so tarjan's own working directory is untouched, and pulls the
// tracking branch git recorded at clone time (no refspec is passed, so a
// branch name can never be misread as a git option).
func pull(ctx context.Context, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", dest, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// clone clones r into dest, returning git's combined output so the caller can
// print it atomically (parallel clones would otherwise interleave live streams).
func clone(ctx context.Context, r config.Repo, dest string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", cloneArgs(r, dest)...)
	return cmd.CombinedOutput()
}

// cloneArgs builds the argv for cloning r into dest. It hardens the invocation
// against a hostile config: `-c protocol.ext.allow=never` blocks git's `ext::`
// transport (which would run an arbitrary shell command from the URL), and the
// `--` terminator ensures a `-`-prefixed URL or branch is treated as a
// positional argument rather than an injected git flag.
func cloneArgs(r config.Repo, dest string) []string {
	args := []string{"-c", "protocol.ext.allow=never", "clone", "--depth", "1"}
	if r.Branch != "" {
		args = append(args, "--branch", r.Branch)
	}
	return append(args, "--", r.URL, dest)
}

// isGitRepo reports whether dir holds a git checkout. It accepts both a ".git"
// directory (a normal clone) and a ".git" file (a linked worktree or submodule),
// so an existing worktree checkout isn't mistaken for "not cloned" and then
// re-cloned into a non-empty directory.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
