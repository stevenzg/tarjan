package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/remote"
	"github.com/stevenzg/tarjan/internal/state"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var downWorkspace string

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop a running environment",
	Long: `down stops the services recorded for a workspace. With no --workspace it
targets the most recently started one.

This is used to stop an environment that was started by another terminal; in
the same terminal, Ctrl+C on 'tarjan up' already tears everything down.`,
	Args: cobra.NoArgs,
	RunE: runDown,
}

func init() {
	downCmd.Flags().StringVarP(&downWorkspace, "workspace", "w", "", "workspace dir to stop (default: most recent)")
	rootCmd.AddCommand(downCmd)
}

func runDown(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	wsDir, err := workspace.Resolve(cfg, downWorkspace)
	if err != nil {
		return err
	}
	st, err := state.Load(wsDir)
	if err != nil {
		ui.Warn("no running environment recorded at %s", wsDir)
		return nil
	}

	ui.Info("stopping %s (%s)", st.Name, wsDir)
	// Reverse order so dependents stop before their dependencies.
	failed := 0
	for i := len(st.Services) - 1; i >= 0; i-- {
		s := st.Services[i]
		ui.Step("stopping %s", s.Name)
		if s.Docker && s.Container != "" {
			// A remote docker service ran against the remote daemon, so target
			// that daemon (via DOCKER_HOST) — otherwise stop/rm hit the local
			// daemon, find nothing, and falsely report success while the remote
			// container keeps running.
			env := dockerEnvFor(cfg, s.Remote)
			// stop lets `--rm` remove a running container gracefully; the
			// follow-up rm -f clears one that had already exited without being
			// removed (a hard kill or daemon restart), which stop alone leaves.
			// A container that no longer exists makes `rm` fail harmlessly; only
			// treat it as an error if the container is still present afterwards.
			_ = dockerCmd(env, "stop", "-t", "5", "--", s.Container).Run()
			_ = dockerCmd(env, "rm", "-f", "--", s.Container).Run()
			if containerExists(env, s.Container) {
				ui.Warn("%s: container %s is still present", s.Name, s.Container)
				failed++
			}
			continue
		}
		if s.PID > 0 {
			// Skip a PID that is already gone: acting on it risks signalling an
			// unrelated process that reused the number since state was written.
			if !pidAlive(s.PID) {
				continue
			}
			stopPID(s.PID)
			// Escalate to SIGKILL if it ignores SIGTERM within the grace window,
			// so a process that traps TERM isn't left running holding its port —
			// the same TERM→grace→KILL discipline the in-process Shutdown uses.
			if !waitPIDGone(s.PID, 8*time.Second) {
				ui.Warn("%s: did not stop within grace; force-killing", s.Name)
				killPID(s.PID)
			}
		}
	}
	state.Remove(wsDir)
	if failed > 0 {
		ui.Warn("stopped with %d issue(s); some resources may still be running", failed)
	} else {
		ui.Success("stopped")
	}
	_ = os.Stdout.Sync()
	return nil
}

// dockerEnvFor returns the environment for docker CLI commands acting on a
// service's container: the remote daemon (via DOCKER_HOST) when the service ran
// on a named remote, or nil (inherit, i.e. the local daemon) otherwise.
func dockerEnvFor(cfg *config.Config, remoteName string) []string {
	if remoteName == "" {
		return nil
	}
	rem, ok := cfg.Remotes[remoteName]
	if !ok {
		return nil
	}
	if h := remote.DockerHost(rem); h != "" {
		return append(os.Environ(), "DOCKER_HOST="+h)
	}
	return nil
}

// dockerCmd builds a docker CLI command with the given environment (nil = local
// daemon).
func dockerCmd(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("docker", args...)
	cmd.Env = env
	return cmd
}

// containerExists reports whether a docker container with the given name is
// still present (running or stopped) on the targeted daemon. A docker/CLI error
// is treated as "not present" so a missing daemon doesn't turn teardown into a
// false failure.
func containerExists(env []string, name string) bool {
	cmd := dockerCmd(env, "ps", "-aq", "-f", "name=^"+name+"$")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

// waitPIDGone polls until the process group is gone or the grace window elapses,
// reporting whether it exited. It is used to decide whether to escalate a
// SIGTERM to SIGKILL.
func waitPIDGone(pid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !pidAlive(pid)
}
