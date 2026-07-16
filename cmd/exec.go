package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var execWorkspace string

var execCmd = &cobra.Command{
	Use:   "exec <service> [-- command args...]",
	Short: "Run a command in a service's working dir with its environment",
	Long: `exec runs a one-off command using a service's working directory and its
fully-resolved environment (env files + inline env) — handy for tests, scripts,
psql, or an interactive shell, without re-exporting variables by hand.

With no command it opens a shell. For a docker service whose container is
running, it runs inside the container (docker exec); otherwise it runs locally.

  tarjan exec api -- npm run test
  tarjan exec api -- psql "$DATABASE_URL"
  tarjan exec api                       # open a shell in api's context`,
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	RunE:                  runExec,
}

func init() {
	execCmd.Flags().StringVarP(&execWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	rootCmd.AddCommand(execCmd)
}

func runExec(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	name := args[0]
	command := args[1:]
	// Everything after `--` is the command; without `--`, treat trailing args
	// as the command too.
	if dash := cmd.ArgsLenAtDash(); dash >= 1 {
		command = args[dash:]
	}

	spec, ok := findService(cfg, name)
	if !ok {
		return fmt.Errorf("unknown service %q (services: %s)", name, strings.Join(serviceNamesOf(cfg), ", "))
	}

	env, err := cfg.ServiceEnv(spec)
	if err != nil {
		return err
	}

	// Prefer execing into a running container for docker services.
	if spec.Docker != nil {
		container := fmt.Sprintf("tarjan-%s-%s", cfg.Name, spec.Name)
		if containerRunning(container) {
			return runProcess(dockerExec(container, command), nil, "")
		}
		ui.Warn("container %s not running; executing locally", container)
	}

	wsDir, err := workspace.Resolve(cfg, execWorkspace)
	if err != nil {
		return err
	}
	dir := wsDir
	if spec.Workdir != "" {
		dir = filepath.Join(wsDir, spec.Workdir)
	}

	if len(command) == 0 {
		command = []string{defaultShell()}
	}
	return runProcess(command, env, dir)
}

func findService(cfg *config.Config, name string) (config.Service, bool) {
	for _, s := range cfg.Services {
		if s.Name == name {
			return s, true
		}
	}
	return config.Service{}, false
}

func serviceNamesOf(cfg *config.Config) []string {
	out := make([]string, len(cfg.Services))
	for i, s := range cfg.Services {
		out[i] = s.Name
	}
	return out
}

// runProcess runs argv with stdio attached to the terminal. env nil inherits
// the current environment; dir "" keeps the current directory.
func runProcess(argv, env []string, dir string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command to run")
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Env = env
	c.Dir = dir
	return c.Run()
}

func dockerExec(container string, command []string) []string {
	argv := []string{"docker", "exec", "-it", container}
	if len(command) == 0 {
		argv = append(argv, "sh")
		return argv
	}
	return append(argv, command...)
}

func containerRunning(name string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}
