package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/control"
	"github.com/stevenzg/tarjan/internal/deps"
	"github.com/stevenzg/tarjan/internal/gitutil"
	"github.com/stevenzg/tarjan/internal/repocfg"
	"github.com/stevenzg/tarjan/internal/runner"
	"github.com/stevenzg/tarjan/internal/shellx"
	"github.com/stevenzg/tarjan/internal/state"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

// runHooks executes lifecycle hook commands in the workspace directory.
func runHooks(ctx context.Context, label string, cmds []string, dir string) error {
	if len(cmds) == 0 {
		return nil
	}
	ui.Info("%s hooks", label)
	for _, line := range cmds {
		ui.Step("%s", line)
		name, args := shellx.Command(line)
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		c.Env = os.Environ()
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s hook failed (%s): %w", label, line, err)
		}
	}
	return nil
}

var (
	upWorkspace string
	upVersion   string
	upNoStart   bool
	upOnly      []string
	upProfiles  []string
	upNoDeps    bool
	upInstall   bool
	upAI        bool
)

var upCmd = &cobra.Command{
	Use:   "up [service...]",
	Short: "Materialise a workspace and start the environment",
	Long: `up creates a fresh workspace, checks required tools, clones the repos,
generates a VS Code workspace, then starts services concurrently in dependency
order.

With no arguments it starts every service. Name one or more services to start
only those (e.g. "tarjan up studio" or "tarjan up service mobile") — the same as
--only, and the two combine. A service's dependencies are pulled in too (so
"tarjan up studio" also brings up whatever studio dependsOn); disable that with
--no-deps. Activate --profile groups to widen the default selection.

Press Ctrl+C to stop the whole environment.`,
	RunE: runUp,
}

func init() {
	upCmd.Flags().StringVarP(&upWorkspace, "workspace", "w", "", "reuse an existing workspace dir instead of creating a fresh one")
	upCmd.Flags().StringVar(&upVersion, "version", "", "name the workspace <product>-<version> and reuse it if it exists (default: the config's version)")
	upCmd.Flags().BoolVar(&upNoStart, "no-start", false, "prepare the workspace (clone, deps, ide) but do not start services")
	upCmd.Flags().StringSliceVar(&upOnly, "only", nil, "start only these services (comma-separated)")
	upCmd.Flags().StringSliceVar(&upProfiles, "profile", nil, "activate these profiles (comma-separated)")
	upCmd.Flags().BoolVar(&upNoDeps, "no-deps", false, "do not pull in dependencies of selected services")
	upCmd.Flags().BoolVar(&upInstall, "install", false, "install missing/outdated required tools via their provider (install/mise/package)")
	upCmd.Flags().BoolVar(&upAI, "ai", false, "let an agent CLI install tools the providers can't (with --install)")
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Positional arguments name services to start, exactly like --only
	// (e.g. `tarjan up studio mobile`); they combine with the --only flag.
	// Dependencies are still pulled in unless --no-deps is set, so
	// `tarjan up studio` also starts whatever studio dependsOn.
	only := mergeSelectors(upOnly, args)

	// Ctrl+C cancels the whole run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ui.Info("environment: %s", cfg.Name)
	if len(upProfiles) > 0 {
		ui.Info("profiles: %v", upProfiles)
	}
	if len(only) > 0 {
		ui.Info("only: %v", only)
	}

	// Resolve which repos this run covers. Services are selected only after
	// cloning, once any per-repo .tarjan configs have been merged in.
	repos := cfg.SelectRepos(upProfiles)

	// 1. Verify required tools (optionally installing missing ones). Scope the
	// check to the services this run will actually start: a tool tagged with
	// `services:` is only needed when one of them is selected, so a partial run
	// (e.g. the cloud-only `tarjan up studio-cloud`) skips the local backend's
	// docker/dotnet/psql toolchain. The selection is resolved here against the
	// base config so the check still runs before any clone (fail fast); repos a
	// clone contributes are handled separately in mergeRepoConfigs. If the
	// selection can't be resolved yet — an --only name that only a cloned repo's
	// .tarjan config defines — fall back to checking every required tool.
	if upAI && !upInstall {
		ui.Warn("--ai has no effect without --install")
	}
	if len(cfg.Requires) > 0 {
		tools := cfg.Requires
		if sel, selErr := cfg.SelectServices(only, upProfiles, !upNoDeps); selErr == nil {
			tools = cfg.RequiredTools(sel)
		}
		if len(tools) > 0 {
			ui.Info("checking required tools")
			if err := deps.Check(tools, deps.Options{AutoInstall: upInstall, AI: upAI}); err != nil {
				return err
			}
		}
	}

	// 2. Resolve / create the workspace. A config loaded from a repo's own
	// .tarjan directory runs in place: the checkout is the workspace.
	var wsDir string
	switch {
	case upWorkspace != "":
		wsDir = upWorkspace
		if err := os.MkdirAll(wsDir, 0o755); err != nil {
			return err
		}
	case cfg.InPlaceDir() != "":
		wsDir = cfg.InPlaceDir()
		ui.Info("running in place")
	default:
		version := upVersion
		if version == "" {
			version = cfg.Version
		}
		var reused bool
		wsDir, reused, err = workspace.Materialize(cfg, version, time.Now())
		if err != nil {
			return err
		}
		if reused {
			ui.Info("reusing existing workspace")
		}
	}
	ui.Info("workspace: %s", wsDir)

	// 3. Clone repos.
	if len(repos) > 0 {
		ui.Info("cloning repos")
		if err := gitutil.CloneAll(ctx, wsDir, repos); err != nil {
			return err
		}
	}

	// 3b. Merge the .tarjan config any cloned repo carries, then check the
	// tools those configs added.
	if err := mergeRepoConfigs(cfg, wsDir, repos); err != nil {
		return err
	}

	// Resolve which services this run covers.
	services, err := cfg.SelectServices(only, upProfiles, !upNoDeps)
	if err != nil {
		return err
	}
	if len(cfg.Services) > 0 && len(services) == 0 {
		ui.Warn("no services match the given selection")
	}

	// 4. Generate the VS Code workspace.
	if cfg.Workspace.VSCode && len(repos) > 0 {
		path, err := workspace.WriteVSCode(cfg, wsDir, repos)
		if err != nil {
			return err
		}
		ui.Success("VS Code workspace: %s", path)
	}

	if upNoStart || len(services) == 0 {
		ui.Success("workspace ready (services not started)")
		return nil
	}

	// 4b. Refuse to start if a previous environment is still running for this
	// workspace — otherwise its services collide with ours on their ports and
	// surface as a confusing "address already in use". A stale state file with
	// nothing alive behind it is cleared so the run proceeds.
	if err := preflightPreviousRun(wsDir); err != nil {
		return err
	}

	// 4c. Clone repos onto any remote that hosts a process service, so its
	// command finds its checkout.
	if err := cloneRemoteRepos(ctx, cfg, services, repos); err != nil {
		return err
	}

	// Check for a newer tarjan in the background; the notice (if any) is printed
	// once the environment is up, so the probe overlaps with service startup.
	noticeCh := startUpdateNotice(ctx)

	// 5. Pre-up hooks (cloud auth, secrets) before any service starts.
	if err := runHooks(ctx, "pre-up", cfg.Hooks.PreUp, wsDir); err != nil {
		return err
	}

	// 6. Start and supervise the selected services.
	run := runner.New(cfg, wsDir)
	run.SetServices(services)
	// Enable `tarjan reload`: re-read this config (YAML or Starlark), re-merge
	// the cloned repos' .tarjan configs, and re-apply the same selection.
	run.SetReload(func() (*config.Config, error) {
		c, err := loadConfig()
		if err != nil {
			return nil, err
		}
		if _, err := repocfg.Apply(c, wsDir, c.SelectRepos(upProfiles)); err != nil {
			return nil, err
		}
		return c, nil
	}, func(c *config.Config) ([]config.Service, error) {
		return c.SelectServices(only, upProfiles, !upNoDeps)
	})
	if err := run.Up(ctx); err != nil {
		run.Shutdown()
		runPostDown(cfg, wsDir)
		return err
	}
	// Expose a control endpoint so `tarjan restart`/`tarjan status` can drive this
	// running environment from another terminal.
	srv, err := control.Serve(wsDir, run)
	if err != nil {
		ui.Warn("control server unavailable (tarjan restart won't work): %v", err)
	}
	ui.Success("environment is up — press Ctrl+C to stop")
	printUpdateNotice(noticeCh)

	run.Wait(ctx)
	// A second interrupt during teardown means "I don't want to wait": force-kill
	// everything and exit now instead of sitting through the graceful grace period.
	stopForce := forceQuitOnSecondSignal(run)
	defer stopForce()
	srv.Close()
	run.Shutdown()
	runPostDown(cfg, wsDir)
	return nil
}

// forceQuitOnSecondSignal watches for another SIGINT/SIGTERM after the first one
// has begun teardown; on receipt it hard-kills all services and exits. It
// returns a stop function to unregister the handler once teardown finishes
// cleanly.
func forceQuitOnSecondSignal(run *runner.Runner) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			ui.Warn("second interrupt — forcing shutdown")
			run.ForceKillAll()
			ui.Flush()
			os.Exit(130)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}

// preflightPreviousRun refuses to start when an environment is still running
// for wsDir. A live control server (or an orphaned service process left by a
// hard-killed run) means starting again would collide on ports. A state file
// left behind by a clean crash — nothing actually alive — is removed so the
// run can proceed.
func preflightPreviousRun(wsDir string) error {
	st, err := state.Load(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no prior run recorded
		}
		// The state file exists but is unreadable/corrupt. A responsive control
		// server is the definitive signal something is still running; refuse in
		// that case rather than starting a colliding second environment. Without
		// one, treat the bad file as stale, clear it, and proceed.
		if _, cerr := control.Statuses(wsDir); cerr == nil {
			return fmt.Errorf("an environment is already running for this workspace, "+
				"but its state file is unreadable (%v)\n  stop it first: tarjan down%s", err, downHint(wsDir))
		}
		ui.Warn("ignoring unreadable state file at %s: %v", wsDir, err)
		state.Remove(wsDir)
		return nil
	}
	running := runningServices(wsDir, st)
	if len(running) == 0 {
		// Stale state from an earlier crash; clear it and carry on.
		state.Remove(wsDir)
		return nil
	}
	ago := ""
	if !st.StartedAt.IsZero() {
		ago = fmt.Sprintf(", started %s ago", time.Since(st.StartedAt).Round(time.Second))
	}
	return fmt.Errorf("an environment is already running for this workspace (%s%s)\n"+
		"  still up: %s\n"+
		"  stop it first: tarjan down%s",
		st.Name, ago, strings.Join(running, ", "), downHint(wsDir))
}

// runningServices returns the names of services from a prior run that are still
// alive. A responsive control server is the definitive signal the supervisor is
// up; failing that, any recorded local process whose PID is still alive is an
// orphan from a hard-killed run.
func runningServices(wsDir string, st *state.State) []string {
	if _, err := control.Statuses(wsDir); err == nil {
		names := make([]string, 0, len(st.Services))
		for _, s := range st.Services {
			names = append(names, s.Name)
		}
		if len(names) == 0 {
			names = append(names, "services")
		}
		return names
	}
	var alive []string
	for _, s := range st.Services {
		if !s.Docker && s.PID > 0 && pidAlive(s.PID) {
			alive = append(alive, s.Name)
		}
	}
	return alive
}

// downHint suggests the -w flag when the workspace isn't the one `tarjan down`
// targets by default (in-place or most-recent).
func downHint(wsDir string) string {
	cfg, err := loadConfig()
	if err != nil {
		return " -w " + wsDir
	}
	if def, err := workspace.Resolve(cfg, ""); err == nil && def == wsDir {
		return ""
	}
	return " -w " + wsDir
}

// mergeRepoConfigs folds the .tarjan config of each cloned repo into cfg,
// reports what was contributed, and checks any tools those configs require.
func mergeRepoConfigs(cfg *config.Config, wsDir string, repos []config.Repo) error {
	sum, err := repocfg.Apply(cfg, wsDir, repos)
	if err != nil {
		return err
	}
	for _, m := range sum.Merged {
		if len(m.Services) > 0 {
			ui.Info("repo %s: merged %d service(s) from %s", m.Repo, len(m.Services), config.RepoConfigDir)
		}
		for _, name := range m.Skipped {
			ui.Warn("repo %s: service %q already defined; keeping the parent config's definition", m.Repo, name)
		}
		for _, tool := range m.SkippedTools {
			ui.Warn("repo %s: tool %s; keeping the already-required version", m.Repo, tool)
		}
	}
	if tools := sum.Tools(); len(tools) > 0 {
		ui.Info("checking tools required by repo configs")
		if err := deps.Check(tools, deps.Options{AutoInstall: upInstall, AI: upAI}); err != nil {
			return err
		}
	}
	return nil
}

// runPostDown runs post-down hooks, using a fresh context since the run's
// context is typically already cancelled by Ctrl+C at this point.
func runPostDown(cfg *config.Config, wsDir string) {
	if len(cfg.Hooks.PostDown) == 0 {
		return
	}
	if err := runHooks(context.Background(), "post-down", cfg.Hooks.PostDown, wsDir); err != nil {
		ui.Warn("%v", err)
	}
}
