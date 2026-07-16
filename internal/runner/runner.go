// Package runner starts, supervises and stops the services that make up an
// environment. Services start concurrently — each gated on its dependencies'
// health — are kept alive per their restart policy, stream prefixed logs to the
// terminal and to per-service log files, and stop in reverse dependency order.
// A running environment can be reconciled against an edited config via Reload.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/remote"
	"github.com/stevenzg/tarjan/internal/shellx"
	"github.com/stevenzg/tarjan/internal/state"
	"github.com/stevenzg/tarjan/internal/ui"
)

// LogsDir returns the directory holding per-service log files for a workspace.
func LogsDir(workspace string) string {
	return filepath.Join(workspace, ".tarjan", "logs")
}

// Selector resolves the desired services from a (re)loaded config — typically
// Config.SelectServices bound to the run's --only/--profile flags.
type Selector func(*config.Config) ([]config.Service, error)

// Runner supervises a single environment instance in a workspace.
type Runner struct {
	workspace string
	logsDir   string

	// loader and selector enable Reload to re-read and re-select. loader
	// dispatches by config format (YAML vs Starlark) and is supplied by the CLI.
	loader   func() (*config.Config, error)
	selector Selector

	reloadMu sync.Mutex // serialises reconciles

	// stopping is set once Shutdown begins, so an in-flight reload never starts
	// fresh services or resurrects the state file after teardown has removed it.
	stopping atomic.Bool

	// mu guards cfg, supervisors, order, nextColor, runCtx and startedAt, which
	// Reload mutates concurrently with running supervisors that read them.
	mu          sync.Mutex
	cfg         *config.Config
	supervisors map[string]*supervisor // by service name
	order       []config.Service       // dependency (topological) order
	nextColor   int
	runCtx      context.Context // the context passed to Up, used to launch reloaded services
	startedAt   time.Time
	lastSaved   time.Time // last successful saveState, for throttling during startup
}

// New creates a Runner for a materialised workspace.
func New(cfg *config.Config, workspace string) *Runner {
	return &Runner{
		cfg:         cfg,
		workspace:   workspace,
		logsDir:     LogsDir(workspace),
		supervisors: map[string]*supervisor{},
	}
}

// SetServices restricts the run to the given services (already in dependency
// order, e.g. from Config.SelectServices). When unset, Up runs every service.
func (r *Runner) SetServices(services []config.Service) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = services
}

// SetReload configures live reload: a loader that re-reads the config and the
// selector that turns it into the desired service set.
func (r *Runner) SetReload(loader func() (*config.Config, error), selector Selector) {
	r.loader = loader
	r.selector = selector
}

// shellCommand returns the OS shell invocation for a command string.
func shellCommand(command string) (string, []string) {
	return shellx.Command(command)
}

// config returns the current config under the lock.
func (r *Runner) config() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// supervisor looks up a supervisor by name under the lock.
func (r *Runner) supervisor(name string) *supervisor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.supervisors[name]
}

// allSupervisors returns a snapshot of the current supervisors.
func (r *Runner) allSupervisors() []*supervisor {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*supervisor, 0, len(r.supervisors))
	for _, s := range r.supervisors {
		out = append(out, s)
	}
	return out
}

// Up launches every service concurrently. Each waits for its dependencies to
// become healthy before starting, so dependency order is preserved without
// serialising independent services. Up returns once every service has become
// healthy (or, for a non-optional service, has permanently failed to).
func (r *Runner) Up(ctx context.Context) error {
	r.mu.Lock()
	r.runCtx = ctx
	order := r.order
	if order == nil {
		o, err := r.cfg.StartOrder()
		if err != nil {
			r.mu.Unlock()
			return err
		}
		order = o
		r.order = o
	}
	r.mu.Unlock()

	if err := os.MkdirAll(r.logsDir, 0o755); err != nil {
		return err
	}

	r.sweepOrphanContainers(ctx)

	// Build and launch a supervisor per service. The map is populated fully
	// before any goroutine starts so dependents can find their dependencies.
	r.mu.Lock()
	sups := make([]*supervisor, len(order))
	for i, spec := range order {
		s := newSupervisor(r, spec, i)
		r.supervisors[spec.Name] = s
		sups[i] = s
	}
	r.nextColor = len(order)
	r.mu.Unlock()

	for _, s := range sups {
		go s.supervise(ctx)
	}

	// Wait for each service's first outcome (healthy, or gave up before ever
	// becoming healthy). Persist state as services come up.
	r.mu.Lock()
	r.startedAt = time.Now()
	r.mu.Unlock()
	for _, s := range sups {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.firstResult:
			if err != nil {
				if s.spec.Optional {
					ui.Warn("optional service %q did not come up: %v", s.spec.Name, err)
					continue
				}
				return fmt.Errorf("service %q failed to start: %w", s.spec.Name, err)
			}
			// Persist as services come up (so `down`/`status` from another terminal
			// can find a partially-started run) but throttle it: saving on every one
			// of N services would rewrite the whole state file — an O(N) locked
			// snapshot each time — N times over. A final unconditional save below
			// captures the complete set.
			r.saveStateThrottled(500 * time.Millisecond)
		}
	}
	r.saveState()
	return nil
}

// sweepOrphanContainers force-removes containers left behind by services that
// are no longer in the config (removed or renamed) for this environment. Stale
// containers for services that still exist are reclaimed per-service in
// startProcess; this catches the orphans that name-matching cannot find,
// keyed off the tarjan.env label set on every container. Names are matched
// against the FULL config — not just this run's selected subset — so a partial
// `up --only …` never removes a still-configured service another session runs.
func (r *Runner) sweepOrphanContainers(ctx context.Context) {
	cfg := r.config()
	expected := map[string]bool{}
	for _, s := range cfg.Services {
		if s.Docker != nil {
			expected[fmt.Sprintf("tarjan-%s-%s", cfg.Name, s.Name)] = true
		}
	}
	if len(expected) == 0 {
		return // no docker services — don't require docker to be installed
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=tarjan.env="+cfg.Name,
		"--format", "{{.Names}}").Output()
	if err != nil {
		return // docker unavailable; a real run failure will surface later
	}
	for _, name := range strings.Fields(string(out)) {
		if expected[name] {
			continue // still a configured service; left for per-service reuse
		}
		ui.Step("removing orphaned container %s", name)
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	}
}

// saveStateThrottled persists the state only if at least minInterval has passed
// since the last save, coalescing the burst of writes as many services come up
// at once during startup. Callers that need a guaranteed write (e.g. the final
// save once Up finishes, or a reload) use saveState directly.
func (r *Runner) saveStateThrottled(minInterval time.Duration) {
	r.mu.Lock()
	recent := !r.lastSaved.IsZero() && time.Since(r.lastSaved) < minInterval
	r.mu.Unlock()
	if recent {
		return
	}
	r.saveState()
}

// saveState persists the current service set to the workspace state file. It is
// a no-op once shutdown has begun, so a late reload never rewrites the state
// file that Shutdown removed on its way out.
func (r *Runner) saveState() {
	if r.stopping.Load() {
		return
	}
	st := &state.State{
		Name:      r.config().Name,
		Workspace: r.workspace,
		StartedAt: r.started(),
		Services:  r.Status(),
	}
	if err := state.Save(r.workspace, st); err != nil {
		// Surface it: without the state file, `tarjan down`/`status` from another
		// terminal can't find or stop this run.
		ui.Warn("could not persist environment state: %v", err)
		return
	}
	r.mu.Lock()
	r.lastSaved = time.Now()
	r.mu.Unlock()
}

// started returns the environment's start time under the lock.
func (r *Runner) started() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startedAt
}

// Reload re-reads the config and reconciles the running environment to it:
// added services start, removed services stop, and changed services restart
// with their new spec. Config errors are returned synchronously; the reconcile
// itself runs in the background and logs its progress.
func (r *Runner) Reload() error {
	if r.loader == nil || r.selector == nil {
		return fmt.Errorf("reload is not configured for this run")
	}
	if r.stopping.Load() {
		return fmt.Errorf("reload: environment is shutting down")
	}
	newCfg, err := r.loader()
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	desired, err := r.selector(newCfg)
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	go r.reconcile(newCfg, desired)
	return nil
}

// reconcile applies the desired service set to the running environment.
func (r *Runner) reconcile(newCfg *config.Config, desired []config.Service) {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	// Shutdown may have started while this reconcile was queued behind reloadMu;
	// bail out rather than spin up services the teardown won't know to stop.
	if r.stopping.Load() {
		return
	}

	desiredByName := make(map[string]config.Service, len(desired))
	for _, s := range desired {
		desiredByName[s.Name] = s
	}

	r.mu.Lock()
	r.cfg = newCfg
	var toStop []*supervisor
	var toStart []config.Service
	for name, sup := range r.supervisors {
		nd, ok := desiredByName[name]
		if !ok {
			toStop = append(toStop, sup) // removed
			continue
		}
		if !reflect.DeepEqual(sup.spec, nd) {
			toStop = append(toStop, sup) // changed: replace
			toStart = append(toStart, nd)
		}
	}
	for _, s := range desired {
		if _, ok := r.supervisors[s.Name]; !ok {
			toStart = append(toStart, s) // added
		}
	}
	for _, sup := range toStop {
		delete(r.supervisors, sup.spec.Name)
	}
	newSups := make([]*supervisor, 0, len(toStart))
	for _, spec := range toStart {
		s := newSupervisor(r, spec, r.nextColor)
		r.nextColor++
		r.supervisors[spec.Name] = s
		newSups = append(newSups, s)
	}
	r.order = desired
	ctx := r.runCtx
	r.mu.Unlock()

	if len(toStop) == 0 && len(newSups) == 0 {
		ui.Info("reload: no changes")
		return
	}

	// Stop removed/changed services and wait for them to exit before starting
	// replacements, so a restarted service doesn't collide with its old port.
	var wg sync.WaitGroup
	for _, sup := range toStop {
		ui.Step("reload: stopping %s", sup.spec.Name)
		wg.Add(1)
		go func(s *supervisor) { defer wg.Done(); s.stop(); <-s.dead }(sup)
	}
	if !waitWithTimeout(&wg, 12*time.Second) {
		// A service that ignored SIGTERM within the grace window would otherwise
		// leak as an orphan still holding its port while its replacement starts.
		// Escalate to SIGKILL — the same TERM→grace→KILL discipline Shutdown uses.
		for _, sup := range toStop {
			select {
			case <-sup.dead:
			default:
				ui.Warn("reload: %s did not stop in time; force-killing", sup.spec.Name)
				sup.forceKill()
			}
		}
		waitWithTimeout(&wg, 5*time.Second)
	}

	for _, sup := range newSups {
		ui.Step("reload: starting %s", sup.spec.Name)
		go sup.supervise(ctx)
	}
	r.saveState()
	ui.Success("reload: %d started, %d stopped", len(newSups), len(toStop))
}

// waitWithTimeout waits for wg, reporting true if it completed before d elapsed
// and false on timeout.
func waitWithTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Wait blocks until ctx is cancelled (Ctrl+C) or every supervisor has exited
// permanently (e.g. all services were finite or crashed past their restart
// limit). A single service crashing no longer tears down the environment.
//
// It re-snapshots the supervisor set each time the current batch all die, so a
// reload that replaces every service doesn't look like "everything stopped":
// reload registers the replacement supervisors before stopping the old ones.
func (r *Runner) Wait(ctx context.Context) {
	for {
		var live []*supervisor
		for _, s := range r.allSupervisors() {
			select {
			case <-s.dead:
			default:
				live = append(live, s)
			}
		}
		if len(live) == 0 {
			ui.Warn("all services have stopped")
			return
		}

		batchDead := make(chan struct{})
		go func() {
			var wg sync.WaitGroup
			for _, s := range live {
				wg.Add(1)
				go func(s *supervisor) { defer wg.Done(); <-s.dead }(s)
			}
			wg.Wait()
			close(batchDead)
		}()

		select {
		case <-ctx.Done():
			return
		case <-batchDead:
			// Loop: pick up any services added since (e.g. via reload).
		}
	}
}

// Shutdown stops every service in reverse dependency order: SIGTERM, a grace
// period, then SIGKILL. Docker containers are stopped via `docker stop`.
func (r *Runner) Shutdown() {
	// Signal teardown before snapshotting so a concurrent reload stops starting
	// new services and won't resurrect the state file we remove below.
	r.stopping.Store(true)

	r.mu.Lock()
	order := append([]config.Service(nil), r.order...)
	byName := make(map[string]*supervisor, len(r.supervisors))
	all := make([]*supervisor, 0, len(r.supervisors))
	for name, s := range r.supervisors {
		byName[name] = s
		all = append(all, s)
	}
	r.mu.Unlock()

	ui.Info("shutting down...")
	for i := len(order) - 1; i >= 0; i-- {
		s := byName[order[i].Name]
		if s == nil {
			continue
		}
		ui.Step("stopping %s", s.spec.Name)
		s.stop()
	}

	// Grace period, then force-kill anything still alive.
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, s := range all {
			wg.Add(1)
			go func(s *supervisor) { defer wg.Done(); <-s.dead }(s)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		for _, s := range all {
			s.forceKill()
		}
	}
	state.Remove(r.workspace)
	ui.Success("environment stopped")
}

// ForceKillAll immediately SIGKILLs every service (and `docker kill`s any
// container), skipping the graceful grace period. It backs the "second Ctrl+C"
// hard stop.
func (r *Runner) ForceKillAll() {
	r.stopping.Store(true)
	for _, s := range r.allSupervisors() {
		s.forceKill()
	}
}

// RestartService triggers a manual restart of a running service. It is the
// in-process entrypoint used by the control server behind `tarjan restart`.
func (r *Runner) RestartService(name string) error {
	s := r.supervisor(name)
	if s == nil {
		return fmt.Errorf("unknown service %q", name)
	}
	if s.spec.External {
		return fmt.Errorf("%q is external; there is nothing to restart", name)
	}
	select {
	case <-s.dead:
		return fmt.Errorf("%q is not running", name)
	default:
	}
	s.requestRestart()
	return nil
}

// ServiceNames returns the names of the services this runner manages, in
// dependency order.
func (r *Runner) ServiceNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	for i, s := range r.order {
		out[i] = s.Name
	}
	return out
}

// Status returns a live snapshot of each service's state, in dependency order.
func (r *Runner) Status() []state.Service {
	r.mu.Lock()
	order := append([]config.Service(nil), r.order...)
	byName := make(map[string]*supervisor, len(r.supervisors))
	for name, s := range r.supervisors {
		byName[name] = s
	}
	r.mu.Unlock()

	out := make([]state.Service, 0, len(order))
	for _, spec := range order {
		if s := byName[spec.Name]; s != nil {
			out = append(out, s.stateEntry())
		}
	}
	return out
}

// IsReady reports whether the named service is currently healthy/ready.
func (r *Runner) IsReady(name string) bool {
	s := r.supervisor(name)
	return s != nil && s.isReady()
}

// remoteFor returns the remote target a service runs on, and whether it targets
// one. A service with an unset (or unknown) remote runs locally.
func (r *Runner) remoteFor(spec config.Service) (config.Remote, bool) {
	if spec.Remote == "" {
		return config.Remote{}, false
	}
	rem, ok := r.config().Remotes[spec.Remote]
	return rem, ok
}

// dockerHost returns the DOCKER_HOST for a docker service that runs on a remote
// daemon, or "" for a local one (or a non-docker service).
func (r *Runner) dockerHost(spec config.Service) string {
	if spec.Docker == nil {
		return ""
	}
	if rem, ok := r.remoteFor(spec); ok {
		return remote.DockerHost(rem)
	}
	return ""
}

// dockerEnviron returns the environment for a docker CLI invocation, adding
// DOCKER_HOST when the service targets a remote daemon. It returns nil (inherit
// the current environment) for a local daemon.
func dockerEnviron(dockerHost string) []string {
	if dockerHost == "" {
		return nil
	}
	return append(os.Environ(), "DOCKER_HOST="+dockerHost)
}

// serviceDir resolves a service's working directory inside the workspace.
func (r *Runner) serviceDir(spec config.Service) string {
	if spec.Workdir == "" {
		return r.workspace
	}
	return filepath.Join(r.workspace, spec.Workdir)
}

// env builds the environment for a service (see config.ServiceEnv), using the
// current config under the lock.
func (r *Runner) env(spec config.Service) ([]string, error) {
	return r.config().ServiceEnv(spec)
}

func logPrefix(name string, idx int) string {
	return ui.ColorFor(idx) + pad(name, 10) + " │" + ui.Reset() + " "
}

// pad truncates or right-pads s to n columns, counting runes rather than bytes
// so a multibyte service name is not sliced mid-rune into a garbled prefix.
func pad(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}
