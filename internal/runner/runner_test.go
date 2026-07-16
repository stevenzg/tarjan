//go:build !windows

package runner

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/state"
)

// waitClosed reports whether ch closes within d.
func waitClosed(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// TestRestartOnFailure verifies a crashing service is restarted up to its limit
// and then gives up.
func TestRestartOnFailure(t *testing.T) {
	ws := t.TempDir()
	counter := filepath.Join(ws, "runs")
	max := 2
	cfg := &config.Config{Name: "t", Services: []config.Service{{
		Name:        "crasher",
		Command:     "printf x >> " + counter + "; exit 1",
		Restart:     "on-failure",
		MaxRestarts: &max,
	}}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if !waitClosed(r.supervisors["crasher"].dead, 10*time.Second) {
		t.Fatal("crasher never gave up")
	}
	data, _ := os.ReadFile(counter)
	// initial run + 2 restarts == 3 executions
	if got := len(strings.TrimSpace(string(data))); got != 3 {
		t.Fatalf("expected 3 runs, got %d (%q)", got, data)
	}
}

// TestCrashIsolation verifies one service crashing does not take down others.
func TestCrashIsolation(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{Name: "faulty", Command: "sleep 0.3; exit 1", Restart: "no"},
		{Name: "steady", Command: "sleep 30", Restart: "no"},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if !waitClosed(r.supervisors["faulty"].dead, 5*time.Second) {
		t.Fatal("faulty service never died")
	}
	if waitClosed(r.supervisors["steady"].dead, 500*time.Millisecond) {
		t.Fatal("steady service died when faulty crashed — crash isolation broken")
	}
	r.Shutdown()
}

// TestDependencyGating verifies a dependent does not start until its dependency
// is healthy.
func TestDependencyGating(t *testing.T) {
	ws := t.TempDir()
	order := filepath.Join(ws, "order")
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{
			Name:    "db",
			Command: "sleep 0.5; printf db >> " + order + "; sleep 30",
			Health:  &config.Health{Command: "grep -q db " + order, Timeout: "5s", Interval: "100ms"},
		},
		{
			Name:      "api",
			Command:   "printf api >> " + order + "; sleep 30",
			DependsOn: []string{"db"},
		},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer r.Shutdown()

	// api starts only after db is healthy, but its write may land just after Up
	// returns; poll until both writes are present.
	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(order)
		if len(data) >= 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(data) != "dbapi" {
		t.Fatalf("dependency order not respected: got %q, want %q", data, "dbapi")
	}
}

// TestLogCapture verifies service output is persisted to a per-service log file.
func TestLogCapture(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{Name: "hi", Command: "echo hello-tarjan", Restart: "no"},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !waitClosed(r.supervisors["hi"].dead, 5*time.Second) {
		t.Fatal("service never finished")
	}

	data, err := os.ReadFile(filepath.Join(LogsDir(ws), "hi.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hello-tarjan") {
		t.Fatalf("log file missing output: %q", data)
	}
}

// TestExternalReachable verifies an external dependency is probed (not started)
// and gates a local dependent once reachable.
func TestExternalReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{Name: "cloud", External: true, Health: &config.Health{TCP: ln.Addr().String(), Timeout: "3s", Interval: "50ms"}},
		{Name: "app", Command: "sleep 30", DependsOn: []string{"cloud"}},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !r.supervisors["cloud"].isReady() {
		t.Fatal("external service not marked reachable")
	}
	if !r.supervisors["app"].isReady() {
		t.Fatal("dependent did not start after external reachable")
	}
	r.Shutdown()
}

// TestExternalUnreachable verifies an unreachable external dependency fails the
// run instead of hanging forever.
func TestExternalUnreachable(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		// Port 1 has nothing listening; a short timeout keeps the test quick.
		{Name: "cloud", External: true, Health: &config.Health{TCP: "127.0.0.1:1", Timeout: "1s", Interval: "100ms"}},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err == nil {
		r.Shutdown()
		t.Fatal("expected Up to fail when external dependency is unreachable")
	}
}

// TestEnvFilePrecedence verifies env files are loaded and that inline env
// overrides them.
func TestEnvFilePrecedence(t *testing.T) {
	ws := t.TempDir()
	dir := t.TempDir() // config dir holding the .env file
	if err := os.WriteFile(filepath.Join(dir, "svc.env"), []byte("FROM_FILE=file\nOVERRIDDEN=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Name: "t", Services: []config.Service{{
		Name:    "envtest",
		Command: "sh -c 'printf \"%s,%s\" \"$FROM_FILE\" \"$OVERRIDDEN\" > " + filepath.Join(ws, "out") + "'",
		EnvFile: []string{"svc.env"},
		Env:     map[string]string{"OVERRIDDEN": "inline"},
	}}}
	cfg.SetDir(dir)

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	<-r.supervisors["envtest"].dead

	data, err := os.ReadFile(filepath.Join(ws, "out"))
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if string(data) != "file,inline" {
		t.Fatalf("env precedence wrong: got %q, want %q", data, "file,inline")
	}
}

// TestJobCompletionGating verifies a dependent starts only after a job exits 0
// (completion-gated, not health-gated).
func TestJobCompletionGating(t *testing.T) {
	ws := t.TempDir()
	order := filepath.Join(ws, "order")
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{
			Name:    "migrate",
			Kind:    "job",
			Command: "sleep 0.4; printf migrate >> " + order,
		},
		{
			Name:      "api",
			Command:   "printf api >> " + order + "; sleep 30",
			DependsOn: []string{"migrate"},
		},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer r.Shutdown()

	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(order)
		if len(data) >= len("migrateapi") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(data) != "migrateapi" {
		t.Fatalf("job did not complete before dependent: got %q, want %q", data, "migrateapi")
	}
}

// TestJobFailureFailsDependents verifies a failed job (non-zero exit) fails the
// run rather than starting dependents.
func TestJobFailureFailsDependents(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{Name: "migrate", Kind: "job", Command: "exit 1"},
		{Name: "api", Command: "sleep 30", DependsOn: []string{"migrate"}},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err == nil {
		r.Shutdown()
		t.Fatal("expected Up to fail when a required job fails")
	}
}

// TestUpFailsOnReadinessTimeout verifies a required service whose process stays
// alive but never passes its health check fails `Up` at the health deadline —
// rather than blocking forever waiting for a service that will never be ready.
func TestUpFailsOnReadinessTimeout(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{{
		Name:    "svc",
		Command: "sleep 30", // alive the whole time
		Health:  &config.Health{Command: "false", Timeout: "500ms", Interval: "50ms"},
	}}}

	r := New(cfg, ws)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := r.Up(ctx)
	r.Shutdown()
	if err == nil {
		t.Fatal("expected Up to fail when a required service never becomes healthy")
	}
	if ctx.Err() != nil {
		t.Fatal("Up should have returned at the health deadline, not the test timeout")
	}
	if !strings.Contains(err.Error(), "never became healthy") {
		t.Fatalf("error = %v, want a readiness-timeout failure", err)
	}
}

// TestOptionalReadinessTimeoutDoesNotFailUp verifies that an *optional* service
// that never becomes healthy is warned about but does not sink `Up`, while a
// required peer still comes up.
func TestOptionalReadinessTimeoutDoesNotFailUp(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{
			Name:     "flaky",
			Optional: true,
			Command:  "sleep 30",
			Health:   &config.Health{Command: "false", Timeout: "500ms", Interval: "50ms"},
		},
		{
			Name:    "good",
			Command: "sleep 30",
			Health:  &config.Health{Command: "true", Timeout: "5s", Interval: "50ms"},
		},
	}}

	r := New(cfg, ws)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up = %v, want nil (optional failure must not sink the env)", err)
	}
	defer r.Shutdown()
	if !r.supervisor("good").isReady() {
		t.Fatal("required service should be ready")
	}
}

// TestManualRestart verifies RestartService restarts a running service in place.
func TestManualRestart(t *testing.T) {
	ws := t.TempDir()
	starts := filepath.Join(ws, "starts")
	cfg := &config.Config{Name: "t", Services: []config.Service{{
		Name:    "svc",
		Command: "sh -c 'printf x >> " + starts + "; sleep 30'",
	}}}

	r := New(cfg, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer r.Shutdown()

	waitForLen := func(n int) bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if data, _ := os.ReadFile(starts); len(data) >= n {
				return true
			}
			time.Sleep(30 * time.Millisecond)
		}
		return false
	}

	if !waitForLen(1) {
		t.Fatal("service never started")
	}
	if err := r.RestartService("svc"); err != nil {
		t.Fatalf("RestartService: %v", err)
	}
	if !waitForLen(2) {
		t.Fatal("service did not restart")
	}
	if err := r.RestartService("ghost"); err == nil {
		t.Fatal("expected error restarting unknown service")
	}
}

func eventually(d time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fn()
}

// TestReloadReconcile verifies that editing the config and reloading adds new
// services, removes deleted ones, and restarts changed ones — all in place.
func TestReloadReconcile(t *testing.T) {
	cfgDir := t.TempDir()
	ws := t.TempDir()
	path := filepath.Join(cfgDir, "tarjan.yaml")
	marker := filepath.Join(ws, "marker")

	v1 := "name: t\nservices:\n" +
		"  - name: a\n    command: \"sleep 300\"\n" +
		"  - name: b\n    command: \"sleep 300\"\n"
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	sel := func(c *config.Config) ([]config.Service, error) { return c.SelectServices(nil, nil, true) }
	svcs, _ := sel(cfg)
	r := New(cfg, ws)
	r.SetServices(svcs)
	r.SetReload(func() (*config.Config, error) { return config.Load(path) }, sel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer r.Shutdown()

	// v2: remove b, add c, change a (now writes a marker on start).
	v2 := "name: t\nservices:\n" +
		"  - name: a\n    command: \"printf a2 > " + marker + "; sleep 300\"\n" +
		"  - name: c\n    command: \"sleep 300\"\n"
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !eventually(5*time.Second, func() bool {
		return r.supervisor("c") != nil && r.IsReady("c") && r.supervisor("b") == nil
	}) {
		t.Fatal("reload did not add c and remove b")
	}
	if !eventually(5*time.Second, func() bool {
		data, _ := os.ReadFile(marker)
		return string(data) == "a2"
	}) {
		t.Fatal("changed service a was not restarted with its new command")
	}
}

// TestReloadRefusedDuringShutdown verifies the reload/teardown ordering: once
// Shutdown has removed the state file, a subsequent reload is refused and does
// not resurrect it.
func TestReloadRefusedDuringShutdown(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "t", Services: []config.Service{
		{Name: "a", Command: "sleep 300"},
	}}
	r := New(cfg, ws)
	r.SetServices(cfg.Services)
	r.SetReload(
		func() (*config.Config, error) { return cfg, nil },
		func(c *config.Config) ([]config.Service, error) { return c.Services, nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	// Up records state while the environment is live.
	if _, err := state.Load(ws); err != nil {
		t.Fatalf("state should exist while up: %v", err)
	}

	r.Shutdown()
	if _, err := state.Load(ws); err == nil {
		t.Fatal("state should be removed after shutdown")
	}
	// A reload after teardown is refused outright...
	if err := r.Reload(); err == nil {
		t.Fatal("Reload after shutdown should error")
	}
	// ...and must not recreate the state file.
	if _, err := state.Load(ws); err == nil {
		t.Fatal("a refused reload must not resurrect the state file")
	}
}

// TestConcurrentReloadAndShutdown stresses the reload/shutdown interaction: a
// burst of reloads racing a teardown must not panic, deadlock, race (run under
// -race), or leave a resurrected state file behind.
func TestConcurrentReloadAndShutdown(t *testing.T) {
	ws := t.TempDir()
	v1 := []config.Service{
		{Name: "a", Command: "sleep 300"},
		{Name: "b", Command: "sleep 300"},
	}
	v2 := []config.Service{
		{Name: "a", Command: "sleep 300"},
		{Name: "c", Command: "sleep 300"},
	}
	r := New(&config.Config{Name: "t", Services: v1}, ws)
	r.SetServices(v1)

	var mu sync.Mutex
	flip := false
	r.SetReload(
		func() (*config.Config, error) {
			mu.Lock()
			defer mu.Unlock()
			flip = !flip
			svcs := v1
			if flip {
				svcs = v2 // alternate the desired set so reconciles do real work
			}
			return &config.Config{Name: "t", Services: svcs}, nil
		},
		func(c *config.Config) ([]config.Service, error) { return c.Services, nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = r.Reload() }()
	}
	r.Shutdown() // races the reload burst
	wg.Wait()
	cancel() // let any supervisor started mid-reconcile observe cancellation

	// Once torn down, the state file stays gone despite any in-flight reconcile
	// (saveState is a no-op after shutdown begins).
	if _, err := state.Load(ws); err == nil {
		t.Fatal("state file present immediately after shutdown")
	}
	if !eventually(2*time.Second, func() bool {
		_, err := state.Load(ws)
		return err != nil
	}) {
		t.Fatal("state file was resurrected by a late reconcile")
	}
}

// TestDockerImageAndRun verifies the docker invocation for a pulled image vs a
// build context (which gets a derived per-service tag), and that built images
// are run like any other container.
func TestDockerImageAndRun(t *testing.T) {
	cfg := &config.Config{Name: "prod", Services: []config.Service{
		{Name: "db", Docker: &config.DockerSpec{Image: "postgres:16", Ports: []string{"5432:5432"}}},
		{Name: "api", Docker: &config.DockerSpec{Build: &config.DockerBuild{Context: "api"}, Ports: []string{"8080:8080"}}},
		{Name: "web", Docker: &config.DockerSpec{Image: "myorg/web:dev", Build: &config.DockerBuild{Context: "web"}}},
	}}
	r := New(cfg, t.TempDir())

	if got := r.dockerImage(cfg.Services[0]); got != "postgres:16" {
		t.Fatalf("pulled image = %q, want postgres:16", got)
	}
	if got := r.dockerImage(cfg.Services[1]); got != "tarjan-prod-api:dev" {
		t.Fatalf("build image tag = %q, want tarjan-prod-api:dev", got)
	}
	// An explicit Image alongside Build names the tag the build produces.
	if got := r.dockerImage(cfg.Services[2]); got != "myorg/web:dev" {
		t.Fatalf("build+image tag = %q, want myorg/web:dev", got)
	}

	_, args := r.dockerRun(cfg.Services[1], "tarjan-prod-api")
	if last := args[len(args)-1]; last != "tarjan-prod-api:dev" {
		t.Fatalf("docker run image arg = %q, want tarjan-prod-api:dev", last)
	}
	// Every container is labelled with its env and service so orphaned
	// containers (from removed/renamed services) can be swept on the next up.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--label tarjan.env=prod") {
		t.Fatalf("missing env label in docker run args: %q", joined)
	}
	if !strings.Contains(joined, "--label tarjan.service=api") {
		t.Fatalf("missing service label in docker run args: %q", joined)
	}
}

// TestDockerCommandOverride verifies a docker service's command override is
// placed after the image, overriding the image's default CMD.
func TestDockerCommandOverride(t *testing.T) {
	svc := config.Service{Name: "agents", Docker: &config.DockerSpec{
		Image:   "agents:dev",
		Command: []string{"uvicorn", "app:main", "--port", "8000"},
	}}
	r := New(&config.Config{Name: "t", Services: []config.Service{svc}}, t.TempDir())

	_, args := r.dockerRun(svc, "tarjan-t-agents")
	joined := strings.Join(args, " ")
	if !strings.HasSuffix(joined, "agents:dev uvicorn app:main --port 8000") {
		t.Fatalf("command override not appended after image: %q", joined)
	}
}
