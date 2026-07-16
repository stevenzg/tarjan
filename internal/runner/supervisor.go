package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/deps"
	"github.com/stevenzg/tarjan/internal/remote"
	"github.com/stevenzg/tarjan/internal/state"
	"github.com/stevenzg/tarjan/internal/ui"
)

// supervisor owns the full lifecycle of one service: gating on dependencies,
// one-shot setup, starting the process/container, health probing, restarting on
// crash (per policy) or on file changes (watch), and clean shutdown.
type supervisor struct {
	runner   *Runner
	spec     config.Service
	colorIdx int
	prefix   string

	// ready is closed when the service first becomes healthy; dependents block
	// on it. dead is closed when the supervisor exits permanently.
	ready chan struct{}
	dead  chan struct{}
	// firstResult delivers exactly one value: nil on first healthy, or an error
	// if the service gives up before ever becoming healthy. Buffered so the
	// supervisor never blocks if Up has already moved on.
	firstResult chan error

	readyOnce sync.Once
	deadOnce  sync.Once
	firstOnce sync.Once
	stopOnce  sync.Once

	stopped   atomic.Bool   // set during Shutdown to suppress restarts
	stopCh    chan struct{} // closed by stop() to unblock external/idle waits
	restartCh chan struct{} // signalled by requestRestart() for a manual restart

	mu        sync.Mutex
	cmd       *exec.Cmd
	container string
	logFile   *os.File
	logBuf    *bufio.Writer // buffers logFile writes; flushed periodically and on close
	logFlush  chan struct{} // closed to stop the periodic flusher
	tunnels   []*exec.Cmd   // ssh -L port-forwards for a remote service
}

func newSupervisor(r *Runner, spec config.Service, idx int) *supervisor {
	return &supervisor{
		runner:      r,
		spec:        spec,
		colorIdx:    idx,
		prefix:      logPrefix(spec.Name, idx),
		ready:       make(chan struct{}),
		dead:        make(chan struct{}),
		stopCh:      make(chan struct{}),
		restartCh:   make(chan struct{}, 1),
		firstResult: make(chan error, 1),
	}
}

func (s *supervisor) markReady()          { s.readyOnce.Do(func() { close(s.ready) }); s.signalFirst(nil) }
func (s *supervisor) markDead()           { s.deadOnce.Do(func() { close(s.dead) }) }
func (s *supervisor) signalFirst(e error) { s.firstOnce.Do(func() { s.firstResult <- e }) }

func (s *supervisor) isReady() bool {
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

// supervise is the goroutine driving the service from gating to permanent exit.
func (s *supervisor) supervise(ctx context.Context) {
	defer s.markDead()

	if err := s.waitForDeps(ctx); err != nil {
		s.signalFirst(err)
		return
	}

	// External services are not started — only probed for reachability, then
	// held "ready" until shutdown so local dependents can rely on them.
	if s.spec.External {
		s.superviseExternal(ctx)
		return
	}

	if err := s.runSetup(ctx); err != nil {
		s.signalFirst(fmt.Errorf("setup: %w", err))
		return
	}
	if err := s.openLog(); err != nil {
		s.signalFirst(err)
		return
	}
	defer s.closeLog()

	if s.spec.IsJob() {
		s.superviseJob(ctx)
		return
	}

	// For a remote service, open the port-forward tunnels once (not per restart)
	// so localhost health checks and local dependents can reach it.
	if rem, ok := s.runner.remoteFor(s.spec); ok {
		s.startTunnels(ctx, rem)
		defer s.stopTunnels()
	}
	s.runLoop(ctx)
}

// superviseJob runs a one-shot job to completion. Readiness — the signal its
// dependents wait on — is a successful (exit 0) completion. A non-zero exit
// fails the job (and any dependents), and the job is never restarted.
func (s *supervisor) superviseJob(ctx context.Context) {
	ui.Info("running job %s", s.spec.Name)
	cmd, streams, err := s.startProcess(ctx)
	if err != nil {
		s.signalFirst(fmt.Errorf("start: %w", err))
		return
	}
	_, _, exitErr := s.waitProcess(ctx, cmd, streams)
	if ctx.Err() != nil || s.stopped.Load() {
		return
	}
	if exitErr != nil {
		s.signalFirst(fmt.Errorf("job %q failed: %w", s.spec.Name, exitErr))
		ui.Error("job %s failed: %v", s.spec.Name, exitErr)
		return
	}
	ui.Success("job %s completed", s.spec.Name)
	s.markReady()
}

// superviseExternal probes a cloud/remote dependency for reachability and, on
// success, keeps it marked ready until the context is cancelled. It starts no
// process, so there is nothing to restart or stop.
func (s *supervisor) superviseExternal(ctx context.Context) {
	ui.Info("checking %s (external)", s.spec.Name)
	if err := waitHealthy(ctx, s.spec.Health); err != nil {
		s.signalFirst(fmt.Errorf("external dependency %q not reachable: %w", s.spec.Name, err))
		return
	}
	ui.Success("%s reachable", s.spec.Name)
	s.markReady()
	select {
	case <-ctx.Done():
	case <-s.stopCh:
	}
}

// waitForDeps blocks until every dependency is healthy. If a dependency dies
// before becoming healthy, this service fails too.
func (s *supervisor) waitForDeps(ctx context.Context) error {
	for _, dep := range s.spec.DependsOn {
		d := s.runner.supervisor(dep)
		if d == nil {
			continue
		}
		select {
		case <-d.ready:
		case <-d.dead:
			if !d.isReady() {
				return fmt.Errorf("dependency %q failed", dep)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// runLoop starts the process and restarts it according to policy/watch until
// the context is cancelled, the service is stopped, or restarts are exhausted.
func (s *supervisor) runLoop(ctx context.Context) {
	restarts := 0
	for {
		if ctx.Err() != nil || s.stopped.Load() {
			return
		}

		cmd, streams, err := s.startProcess(ctx)
		if err != nil {
			s.signalFirst(fmt.Errorf("start: %w", err))
			if !s.decideRestart(ctx, &restarts, false, err) {
				return
			}
			continue
		}
		startedAt := time.Now()

		// Probe health in the background; mark ready (and satisfy Up) on success.
		hctx, hcancel := context.WithCancel(ctx)
		go func() {
			err := waitHealthy(hctx, s.spec.Health)
			if err == nil {
				if !s.isReady() {
					ui.Success("%s ready", s.spec.Name)
				}
				s.markReady()
				return
			}
			// The probe exhausted its own deadline while the process is still
			// alive (hctx is only cancelled once waitProcess returns). That is a
			// genuine readiness-timeout on the first start: report the failure so
			// `up` fails fast instead of blocking forever on a running-but-never-
			// healthy service. A probe cut short by shutdown or by the process
			// exiting leaves hctx cancelled and is handled by the run loop below.
			if hctx.Err() == nil && !s.stopped.Load() && !s.isReady() {
				ui.Error("%s: %v", s.spec.Name, err)
				s.signalFirst(fmt.Errorf("%s never became healthy: %w", s.spec.Name, err))
			}
		}()

		forced, reason, exitErr := s.waitProcess(ctx, cmd, streams)
		hcancel()

		if ctx.Err() != nil || s.stopped.Load() {
			return
		}
		if forced {
			ui.Info("%s: %s, restarting", s.spec.Name, reason)
			continue // forced restarts (watch/manual) bypass the policy/limit
		}

		// A process that stayed up past the stability window was not part of a
		// restart storm, so its exit starts a fresh count: the restart limit and
		// backoff track *consecutive* early failures, not lifetime restarts.
		if time.Since(startedAt) >= stableAfter {
			restarts = 0
		}

		// The process exited on its own (crash isolation: we do not tear down
		// the rest of the environment).
		if !s.decideRestart(ctx, &restarts, true, exitErr) {
			if !s.isReady() {
				s.signalFirst(fmt.Errorf("%s exited before becoming healthy", s.spec.Name))
			}
			return
		}
	}
}

// waitProcess blocks until the process exits or a restart is forced (by a file
// change or a manual request). It returns whether a restart was forced, the
// reason (for logging), and the process's exit error (nil on a clean exit).
// cmd.Wait is called exactly once.
func (s *supervisor) waitProcess(ctx context.Context, cmd *exec.Cmd, streams *sync.WaitGroup) (forced bool, reason string, err error) {
	exit := make(chan error, 1)
	// Drain stdout/stderr fully before reaping: exec.Cmd.Wait closes the pipes,
	// so calling it before reads finish would truncate captured output.
	go func() { streams.Wait(); exit <- cmd.Wait() }()

	var changes <-chan struct{}
	var stopWatch func()
	if s.spec.Watch != nil && len(s.spec.Watch.Paths) > 0 {
		changes, stopWatch = s.startWatcher(ctx)
		defer stopWatch()
	}

	select {
	case e := <-exit:
		return false, "", e
	case <-changes:
		return true, "change detected", s.stopAndReap(cmd, exit)
	case <-s.restartCh:
		return true, "restart requested", s.stopAndReap(cmd, exit)
	case <-ctx.Done():
		return false, "", s.stopAndReap(cmd, exit)
	}
}

// stopAndReap sends SIGTERM (via terminateCmd) and waits for the process to exit,
// escalating to SIGKILL if it does not stop within the grace window. Without the
// escalation a process that ignores SIGTERM would block the run loop on the exit
// channel forever, wedging watch/manual restarts and never freeing its port.
func (s *supervisor) stopAndReap(cmd *exec.Cmd, exit <-chan error) error {
	s.terminateCmd(cmd)
	select {
	case e := <-exit:
		return e
	case <-time.After(forcedStopGrace):
		s.forceKillCmd(cmd)
		return <-exit
	}
}

// requestRestart asks a running service to restart (bypassing its policy/limit).
func (s *supervisor) requestRestart() {
	select {
	case s.restartCh <- struct{}{}:
	default: // a restart is already pending
	}
}

// decideRestart reports whether to restart, logging and backing off as needed.
// crashed distinguishes a process that ran and exited from one that failed to
// start. exitErr is non-nil for a non-zero exit.
func (s *supervisor) decideRestart(ctx context.Context, restarts *int, crashed bool, exitErr error) bool {
	policy := s.spec.Policy()
	switch policy {
	case config.RestartAlways:
		// restart regardless of exit status
	case config.RestartOnFailure:
		if crashed && exitErr == nil {
			ui.Info("%s exited cleanly", s.spec.Name)
			return false
		}
	default: // RestartNo
		if exitErr != nil {
			ui.Warn("%s exited: %v", s.spec.Name, exitErr)
		} else {
			ui.Info("%s exited", s.spec.Name)
		}
		return false
	}

	limit := s.spec.RestartLimit()
	if limit != 0 && *restarts >= limit {
		ui.Warn("%s exceeded restart limit (%d); giving up", s.spec.Name, limit)
		return false
	}
	*restarts++

	delay := backoff(*restarts)
	ui.Warn("%s exited; restarting in %s (attempt %d)", s.spec.Name, delay, *restarts)
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

// forcedStopGrace is how long a forced stop (watch/manual restart, or Ctrl+C)
// waits for a SIGTERM'd process to exit before escalating to SIGKILL.
const forcedStopGrace = 8 * time.Second

// stableAfter is how long a process must stay up before its exit is treated as
// a fresh failure rather than part of a restart storm (resetting the restart
// count and backoff).
const stableAfter = 30 * time.Second

// backoff is a capped exponential delay: 500ms, 1s, 2s, ... up to 10s.
func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << (attempt - 1)
	if d > 10*time.Second || d <= 0 {
		return 10 * time.Second
	}
	return d
}

// launchPlan resolves how a service is launched: the executable and args, the
// local working directory, and the child environment. It covers a local
// process, a docker container (on a local or remote daemon), and a remote
// process run over ssh.
func (s *supervisor) launchPlan(ctx context.Context, spec config.Service, rem config.Remote, isRemote bool) (name string, args []string, dir string, env []string, err error) {
	switch {
	case spec.Docker != nil:
		container := fmt.Sprintf("tarjan-%s-%s", s.runner.config().Name, spec.Name)
		removeStaleContainer(ctx, container, s.runner.dockerHost(spec))
		name, args = s.runner.dockerRun(spec, container)
		s.mu.Lock()
		s.container = container
		s.mu.Unlock()
		if env, err = s.runner.env(spec); err != nil {
			return "", nil, "", nil, err
		}
		if h := s.runner.dockerHost(spec); h != "" {
			env = append(env, "DOCKER_HOST="+h)
			ui.Info("starting %s (docker %s on %s)", spec.Name, s.runner.dockerImage(spec), rem.Target())
		} else {
			ui.Info("starting %s (docker %s)", spec.Name, s.runner.dockerImage(spec))
		}
		return name, args, s.runner.serviceDir(spec), env, nil

	case isRemote:
		extraEnv, eerr := s.runner.config().ServiceExtraEnv(spec)
		if eerr != nil {
			return "", nil, "", nil, eerr
		}
		wd := remote.WorkdirPath(rem.RemoteWorkspace(s.runner.config().Name), spec.Workdir)
		name, args = remote.Invocation(rem, remote.Script(wd, extraEnv, spec.Command, true))
		ui.Info("starting %s (ssh %s)", spec.Name, rem.Target())
		// The ssh client inherits tarjan's environment (nil Env) so it can reach
		// the user's ssh agent and keys; the service's own env is exported inside
		// the remote script, not on the local ssh process.
		return name, args, s.runner.workspace, nil, nil

	default:
		name, args = shellCommand(spec.Command)
		if env, err = s.runner.env(spec); err != nil {
			return "", nil, "", nil, err
		}
		ui.Info("starting %s", spec.Name)
		return name, args, s.runner.serviceDir(spec), env, nil
	}
}

// startProcess launches the service (local process, docker container, or — when
// the service targets a remote — an ssh session / remote-daemon docker run) and
// streams its output to the terminal and log file.
func (s *supervisor) startProcess(ctx context.Context) (*exec.Cmd, *sync.WaitGroup, error) {
	spec := s.spec
	rem, isRemote := s.runner.remoteFor(spec)

	name, args, dir, env, err := s.launchPlan(ctx, spec, rem, isRemote)
	if err != nil {
		return nil, nil, err
	}

	// A remote process runs over ssh, so the local working directory is
	// irrelevant; everything else runs in (and requires) its service directory.
	if dir != s.runner.workspace {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return nil, nil, fmt.Errorf("workdir %q does not exist — is it the path of a declared repo?", spec.Workdir)
		}
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	setProcAttr(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	// Track the readers so the process is only reaped after they reach EOF.
	streams := &sync.WaitGroup{}
	streams.Add(2)
	go func() { defer streams.Done(); s.stream(stdout) }()
	go func() { defer streams.Done(); s.stream(stderr) }()
	return cmd, streams, nil
}

// removeStaleContainer clears any leftover container holding this service's
// name before starting a fresh one. tarjan runs containers with
// `docker run --rm`, which auto-removes them on a clean exit — but a hard kill
// (SIGKILL), a crash, or a Docker daemon restart can leave a stale container
// behind. Its name then blocks the next `docker run` with a
// "container name already in use" conflict. Because the name is deterministic
// (tarjan-<env>-<service>) and owned by tarjan, forcibly reclaiming it is safe.
//
// A single `docker rm -f` does the job idempotently: it removes the container
// if present and errors harmlessly (discarded) if not — one daemon round-trip
// instead of a `docker ps` probe followed by a conditional remove.
func removeStaleContainer(ctx context.Context, container, dockerHost string) {
	rm := exec.CommandContext(ctx, "docker", "rm", "-f", container)
	rm.Env = dockerEnviron(dockerHost)
	_ = rm.Run()
}

// dockerRun builds the `docker run` invocation for a containerised service.
func (r *Runner) dockerRun(spec config.Service, container string) (string, []string) {
	d := spec.Docker
	// Label every container with its environment (and service) so orphans left
	// by services that were later removed or renamed can be swept on the next
	// `up` — the name alone no longer matches any configured service.
	args := []string{
		"run", "--rm", "--name", container,
		"--label", "tarjan.env=" + r.config().Name,
		"--label", "tarjan.service=" + spec.Name,
	}
	for _, p := range d.Ports {
		args = append(args, "-p", p)
	}
	for _, v := range d.Volumes {
		args = append(args, "-v", v)
	}
	for k, val := range d.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, val))
	}
	args = append(args, d.Args...)
	args = append(args, r.dockerImage(spec))
	args = append(args, d.Command...)
	return "docker", args
}

// dockerImage returns the image reference to run for a docker service: the
// pulled Image, or — for a build service with no explicit Image tag — a
// derived, per-service local tag.
func (r *Runner) dockerImage(spec config.Service) string {
	d := spec.Docker
	if d.Build != nil && d.Image == "" {
		return fmt.Sprintf("tarjan-%s-%s:dev", r.config().Name, spec.Name)
	}
	return d.Image
}

// buildDockerImage builds a service's image from its source context. It is run
// once per workspace from runSetup, so a fresh `tarjan up` rebuilds (picking up
// source changes) while in-workspace restarts reuse the built image.
func (r *Runner) buildDockerImage(ctx context.Context, spec config.Service) error {
	d := spec.Docker
	contextDir := filepath.Join(r.workspace, d.Build.Context)
	args := []string{"build", "-t", r.dockerImage(spec)}
	if d.Build.Dockerfile != "" {
		args = append(args, "-f", filepath.Join(contextDir, d.Build.Dockerfile))
	}
	for k, v := range d.Build.Args {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, contextDir)
	if h := r.dockerHost(spec); h != "" {
		ui.Info("building %s image on %s (docker build %s)", spec.Name, h, d.Build.Context)
	} else {
		ui.Info("building %s image (docker build %s)", spec.Name, d.Build.Context)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = r.workspace
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = dockerEnviron(r.dockerHost(spec))
	return cmd.Run()
}

// runSetup runs one-shot setup commands once per workspace (marker-tracked).
// A docker service with a build context is built here too, so the image is
// produced once per workspace alongside any setup commands.
func (s *supervisor) runSetup(ctx context.Context) error {
	// Materialise any tool versions the workdir pins for itself (mise.toml /
	// .tool-versions) before anything runs in it, so setup and command use the
	// repo's own pinned runtime rather than the environment baseline. Not
	// marker-gated (idempotent, and a pull can change the pins) and not part of
	// the early return below — a service with no setup still needs its runtime.
	if _, isRemote := s.runner.remoteFor(s.spec); !isRemote && s.spec.Docker == nil {
		if err := deps.InstallWorkdirPins(ctx, s.spec.Name, s.runner.serviceDir(s.spec)); err != nil {
			return err
		}
	}

	needsBuild := s.spec.Docker != nil && s.spec.Docker.Build != nil
	if len(s.spec.Setup) == 0 && !needsBuild {
		return nil
	}

	// A completed-setup marker records that this workspace's one-shot setup ran.
	// Without a setupCheck the marker alone means done — the original behaviour,
	// and the fast path that avoids resolving env for an already-provisioned
	// service. With a setupCheck we still re-verify below before trusting it.
	marker := filepath.Join(s.runner.workspace, ".tarjan", "setup-"+s.spec.Name)
	_, markerErr := os.Stat(marker)
	markerExists := markerErr == nil
	if markerExists && s.spec.SetupCheck == "" {
		ui.Step("%s setup already done", s.spec.Name)
		return nil
	}

	// Resolve how a setup line (and the optional setupCheck) becomes a command:
	// a remote process service runs on the remote host, in the remote workdir,
	// with its configured env exported into the remote shell; everything else
	// runs locally in the service dir with the service's env.
	rem, isRemote := s.runner.remoteFor(s.spec)
	remoteSetup := isRemote && s.spec.Docker == nil
	var localEnv, extraEnv []string
	var err error
	if remoteSetup {
		if extraEnv, err = s.runner.config().ServiceExtraEnv(s.spec); err != nil {
			return err
		}
	} else if localEnv, err = s.runner.env(s.spec); err != nil {
		return err
	}
	wd := s.runner.serviceDir(s.spec)
	setupCmd := func(line string) *exec.Cmd {
		if remoteSetup {
			rwd := remote.WorkdirPath(rem.RemoteWorkspace(s.runner.config().Name), s.spec.Workdir)
			name, args := remote.Invocation(rem, remote.Script(rwd, extraEnv, line, false))
			return exec.CommandContext(ctx, name, args...)
		}
		name, args := shellCommand(line)
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = wd
		cmd.Env = localEnv
		return cmd
	}

	// runCheck runs the optional setupCheck predicate, returning nil when it
	// exits 0. Output is captured (not streamed) so a passing check stays quiet
	// while a failing one can still surface what it saw.
	runCheck := func() error {
		out, err := setupCmd(s.spec.SetupCheck).CombinedOutput()
		if err != nil {
			if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
				return fmt.Errorf("%w: %s", err, trimmed)
			}
			return err
		}
		return nil
	}

	if markerExists {
		// The marker is present and a setupCheck is declared: re-verify rather
		// than trust it. A passing check confirms the workspace is still
		// provisioned; a failing one means it rotted (e.g. an interrupted install
		// left a package recorded but its postinstall artifacts missing), so drop
		// the marker and re-run setup to self-heal.
		if err := runCheck(); err == nil {
			ui.Step("%s setup already done", s.spec.Name)
			return nil
		}
		ui.Warn("%s: setup check failed, re-running setup", s.spec.Name)
		_ = os.Remove(marker)
	}

	if needsBuild {
		if err := s.runner.buildDockerImage(ctx, s.spec); err != nil {
			return err
		}
	}

	for _, line := range s.spec.Setup {
		ui.Step("%s: %s", s.spec.Name, line)
		cmd := setupCmd(line)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Gate the completed-setup marker on the setupCheck: a setup command that
	// exits 0 without producing what it should (e.g. `npm install` reporting
	// "up to date" while a package's downloaded binary is missing) must not be
	// recorded as done, or the workspace freezes in a broken state a plain
	// re-run can never repair.
	if s.spec.SetupCheck != "" {
		if err := runCheck(); err != nil {
			return fmt.Errorf("setup check %q failed: %w", s.spec.SetupCheck, err)
		}
	}

	return os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0o644)
}

// stream copies a pipe line-by-line to the terminal (prefixed) and the log file.
func (s *supervisor) stream(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		ui.Println(s.prefix + line)
		s.writeLog(line)
	}
}

// logFlushInterval bounds how long a buffered log line waits before hitting
// disk, so `tarjan logs` and the TUI stay reasonably fresh while high-volume
// output is still coalesced into far fewer write syscalls than line-by-line.
const logFlushInterval = 250 * time.Millisecond

func (s *supervisor) openLog() error {
	f, err := os.OpenFile(filepath.Join(s.runner.logsDir, s.spec.Name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	s.mu.Lock()
	s.logFile = f
	s.logBuf = bufio.NewWriter(f)
	s.logFlush = stop
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(logFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.logBuf != nil {
					_ = s.logBuf.Flush()
				}
				s.mu.Unlock()
			}
		}
	}()
	return nil
}

func (s *supervisor) writeLog(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logBuf != nil {
		_, _ = fmt.Fprintln(s.logBuf, line)
	}
}

func (s *supervisor) closeLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFlush != nil {
		close(s.logFlush)
		s.logFlush = nil
	}
	if s.logBuf != nil {
		_ = s.logBuf.Flush()
		s.logBuf = nil
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

// dockerCmd builds a docker CLI command targeting the service's daemon — the
// remote one (via DOCKER_HOST) when the service runs remotely, else the local
// daemon.
func (s *supervisor) dockerCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", args...)
	cmd.Env = dockerEnviron(s.runner.dockerHost(s.spec))
	return cmd
}

// startTunnels opens ssh port-forwards that bring a remote service's published
// ports back to the same localhost port, so local dependents and health checks
// reach it at localhost. It is a no-op for a local service, one with forwarding
// disabled, or one that exposes no forwardable ports.
func (s *supervisor) startTunnels(ctx context.Context, rem config.Remote) {
	if !rem.ForwardEnabled() {
		return
	}
	ports := remote.ForwardPorts(s.spec)
	name, args, ok := remote.TunnelArgs(rem, ports)
	if !ok {
		return
	}
	cmd := exec.CommandContext(ctx, name, args...)
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		ui.Warn("%s: could not open port-forward tunnel: %v", s.spec.Name, err)
		return
	}
	s.mu.Lock()
	s.tunnels = append(s.tunnels, cmd)
	s.mu.Unlock()
	ui.Step("%s: forwarding %v from %s", s.spec.Name, ports, rem.Target())
	// Reap the tunnel when it exits so it never lingers as a zombie.
	go func() { _ = cmd.Wait() }()
}

// stopTunnels terminates any port-forward processes opened for the service.
func (s *supervisor) stopTunnels() {
	s.mu.Lock()
	tunnels := s.tunnels
	s.tunnels = nil
	s.mu.Unlock()
	for _, t := range tunnels {
		kill(t)
	}
}

// stop requests a graceful shutdown of the service (used by Runner.Shutdown).
func (s *supervisor) stop() {
	s.stopped.Store(true)
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.stopTunnels()
	s.mu.Lock()
	cmd, container := s.cmd, s.container
	s.mu.Unlock()
	if container != "" {
		_ = s.dockerCmd("stop", "-t", "5", "--", container).Run()
	}
	if cmd != nil {
		terminate(cmd)
	}
}

func (s *supervisor) terminateCmd(cmd *exec.Cmd) {
	s.mu.Lock()
	container := s.container
	s.mu.Unlock()
	if container != "" {
		_ = s.dockerCmd("stop", "-t", "5", "--", container).Run()
	}
	terminate(cmd)
}

// forceKillCmd SIGKILLs the given process group and, for a docker service,
// `docker kill`s the container — the hard-stop escalation used when SIGTERM was
// ignored within the grace window.
func (s *supervisor) forceKillCmd(cmd *exec.Cmd) {
	s.mu.Lock()
	container := s.container
	s.mu.Unlock()
	if container != "" {
		_ = s.dockerCmd("kill", "--", container).Run()
	}
	if cmd != nil {
		kill(cmd)
	}
}

func (s *supervisor) forceKill() {
	s.stopTunnels()
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	s.forceKillCmd(cmd)
}

func (s *supervisor) stateEntry() state.Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := state.Service{Name: s.spec.Name, Docker: s.container != "", External: s.spec.External, Job: s.spec.IsJob(), Remote: s.spec.Remote}
	if s.container != "" {
		e.Container = s.container
	} else if s.cmd != nil && s.cmd.Process != nil {
		e.PID = s.cmd.Process.Pid
	}
	return e
}
