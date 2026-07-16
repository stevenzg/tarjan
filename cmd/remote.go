package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/remote"
	"github.com/stevenzg/tarjan/internal/ui"
)

// cloneRemoteRepos clones, on each remote that hosts a process (non-docker)
// service, the repos those services run against — so a remote command finds its
// checkout. Docker services need no remote clone: their build context ships to
// the remote daemon over DOCKER_HOST. Cloning is idempotent (it skips an
// existing checkout), and credentials for private repos are the remote host's
// responsibility.
func cloneRemoteRepos(ctx context.Context, cfg *config.Config, services []config.Service, repos []config.Repo) error {
	if len(repos) == 0 {
		return nil
	}
	// Group process-hosting remotes (stable order for deterministic output).
	var order []string
	seen := map[string]bool{}
	for _, s := range services {
		if s.Remote == "" || s.Docker != nil || s.External || seen[s.Remote] {
			continue
		}
		if _, ok := cfg.Remotes[s.Remote]; !ok {
			continue
		}
		seen[s.Remote] = true
		order = append(order, s.Remote)
	}

	for _, name := range order {
		rem := cfg.Remotes[name]
		needed := reposForRemote(name, services, repos)
		if len(needed) == 0 {
			continue
		}
		ui.Info("preparing repos on %s (%s)", name, rem.Target())
		root := rem.RemoteWorkspace(cfg.Name)
		for _, repo := range needed {
			sshName, sshArgs := remote.Invocation(rem, remote.CloneScript(root, repo))
			c := exec.CommandContext(ctx, sshName, sshArgs...)
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("clone %s on remote %q: %w", repo.Name, name, err)
			}
			ui.Step("%s ready on %s", repo.Name, name)
		}
	}
	return nil
}

// reposForRemote returns the repos a remote's process services run against,
// matched by workdir: a service with workdir "api" or "api/src" needs the repo
// checked out at "api". A service with no workdir targets the remote workspace
// root and pulls in no specific repo.
func reposForRemote(remoteName string, services []config.Service, repos []config.Repo) []config.Repo {
	needed := map[string]bool{}
	for _, s := range services {
		if s.Remote != remoteName || s.Docker != nil || s.External || s.Workdir == "" {
			continue
		}
		for _, repo := range repos {
			p := repo.Path()
			if s.Workdir == p || strings.HasPrefix(s.Workdir, p+"/") {
				needed[repo.Name] = true
			}
		}
	}
	out := make([]config.Repo, 0, len(needed))
	for _, repo := range repos {
		if needed[repo.Name] {
			out = append(out, repo)
		}
	}
	return out
}
