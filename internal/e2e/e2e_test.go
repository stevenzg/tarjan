// Package e2e drives the real, compiled tarjan binary through a full lifecycle
// against a self-contained config — no repos to clone and no external tools —
// so the up → healthy → status → teardown path is exercised end to end.
//
// It is skipped under `go test -short` and on Windows (the teardown relies on
// POSIX signals and a `sh`-run service).
package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stevenzg/tarjan/internal/state"
)

// syncBuffer is a concurrency-safe io.Writer: the child process writes to it
// from tarjan's own goroutines while the test reads it after a failure, so it
// must be guarded to stay clean under the race detector.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// buildBinary compiles tarjan into a temp dir once for the test and returns its
// path. The binary is a dev build (no version ldflags), which disables the
// background update check outright.
func buildBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "tarjan")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

// repoRoot walks up from this test file to the module root (two levels up from
// internal/e2e).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func TestUpStatusTeardown(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped under -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("e2e: POSIX signals / sh service; not run on Windows")
	}

	bin := buildBinary(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")

	// A single long-running service whose readiness is a command probe that
	// exits 0 immediately — fully hermetic, no ports, no network.
	cfg := "name: e2e-smoke\n" +
		"workspaceRoot: " + filepath.Join(dir, "root") + "\n" +
		"services:\n" +
		"  - name: svc\n" +
		"    command: \"sleep 30\"\n" +
		"    health:\n" +
		"      command: \"true\"\n" +
		"      timeout: \"20s\"\n" +
		"      interval: \"200ms\"\n"
	cfgPath := filepath.Join(dir, "tarjan.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(), "TARJAN_NO_UPDATE_CHECK=1")

	// Start `tarjan up` in the background against an explicit workspace.
	up := exec.Command(bin, "-c", cfgPath, "up", "-w", ws)
	up.Dir = dir
	up.Env = env
	var out syncBuffer
	up.Stdout, up.Stderr = &out, &out
	if err := up.Start(); err != nil {
		t.Fatalf("start up: %v", err)
	}
	// Guarantee the child is never leaked if an assertion fails midway.
	defer func() {
		if up.Process != nil {
			_ = up.Process.Kill()
			_, _ = up.Process.Wait()
		}
	}()

	// The environment is up once the state file lands (written after the
	// service becomes healthy).
	if !waitFor(10*time.Second, func() bool {
		_, err := state.Load(ws)
		return err == nil
	}) {
		t.Fatalf("environment never came up within 10s\n--- output ---\n%s", out.String())
	}

	st, err := state.Load(ws)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if len(st.Services) != 1 || st.Services[0].Name != "svc" {
		t.Fatalf("recorded services = %+v, want a single svc", st.Services)
	}

	// `tarjan status -w ws` should report the running service via the live
	// control endpoint.
	status := exec.Command(bin, "-c", cfgPath, "status", "-w", ws)
	status.Dir, status.Env = dir, env
	statusOut, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(string(statusOut), "svc") {
		t.Fatalf("status output missing svc:\n%s", statusOut)
	}

	// Ctrl+C: interrupt the up process and expect a clean teardown.
	if err := up.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal up: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- up.Wait() }()
	select {
	case err := <-done:
		// A clean shutdown after SIGINT exits 0 (nil error).
		if err != nil {
			t.Fatalf("up did not shut down cleanly: %v\n--- output ---\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("up did not exit within 15s of SIGINT\n--- output ---\n%s", out.String())
	}

	// Teardown must clear the recorded state so the next `up` starts clean.
	if _, err := state.Load(ws); err == nil {
		t.Fatal("state file should have been removed on teardown")
	}
}

// waitFor polls cond every 50ms until it is true or the timeout elapses.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// skipUnlessE2E centralises the two guards every e2e test shares.
func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e: skipped under -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("e2e: POSIX signals / sh service; not run on Windows")
	}
}

// writeConfig writes a tarjan.yaml with the given services block into dir and
// returns its path. The workspaceRoot is pinned under dir so nothing escapes
// the test's temp tree.
func writeConfig(t *testing.T, dir, name, services string) string {
	t.Helper()
	body := "name: " + name + "\n" +
		"workspaceRoot: " + filepath.Join(dir, "root") + "\n" +
		"services:\n" + services
	path := filepath.Join(dir, "tarjan.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// startUp launches `tarjan up -w ws` in the background and registers a cleanup
// that kills the child if the test leaves it running.
func startUp(t *testing.T, bin, dir, cfgPath, ws string) (*exec.Cmd, *syncBuffer) {
	t.Helper()
	up := exec.Command(bin, "-c", cfgPath, "up", "-w", ws)
	up.Dir = dir
	up.Env = append(os.Environ(), "TARJAN_NO_UPDATE_CHECK=1")
	out := &syncBuffer{}
	up.Stdout, up.Stderr = out, out
	if err := up.Start(); err != nil {
		t.Fatalf("start up: %v", err)
	}
	t.Cleanup(func() {
		if up.Process != nil {
			_ = up.Process.Kill()
			_, _ = up.Process.Wait()
		}
	})
	return up, out
}

// TestUpFailsWhenRequiredServiceUnhealthy verifies the real binary's failure
// contract: when a required service never becomes healthy, `up` exits non-zero
// (so CI scripts and `&&` chains see the failure) rather than hanging or
// reporting success.
func TestUpFailsWhenRequiredServiceUnhealthy(t *testing.T) {
	skipUnlessE2E(t)

	bin := buildBinary(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	// The process stays alive but its readiness probe never passes, so the
	// supervisor gives up at the timeout and Up returns an error.
	cfgPath := writeConfig(t, dir, "e2e-fail",
		"  - name: svc\n"+
			"    command: \"sleep 30\"\n"+
			"    health:\n"+
			"      command: \"false\"\n"+
			"      timeout: \"1s\"\n"+
			"      interval: \"100ms\"\n")

	up, out := startUp(t, bin, dir, cfgPath, ws)

	done := make(chan error, 1)
	go func() { done <- up.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("up exited 0, want a non-zero failure\n--- output ---\n%s", out.String())
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("up failed with %T (%v), want an ExitError", err, err)
		}
		if !strings.Contains(out.String(), "failed to start") {
			t.Fatalf("output missing the failure reason:\n%s", out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("up neither came up nor failed within 15s\n--- output ---\n%s", out.String())
	}
}

// TestOptionalServiceFailureDoesNotSinkEnvironment verifies that a failing
// *optional* service is warned about but the rest of the environment still
// comes up cleanly.
func TestOptionalServiceFailureDoesNotSinkEnvironment(t *testing.T) {
	skipUnlessE2E(t)

	bin := buildBinary(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	cfgPath := writeConfig(t, dir, "e2e-optional",
		"  - name: flaky\n"+
			"    optional: true\n"+
			"    command: \"sleep 30\"\n"+
			"    health:\n"+
			"      command: \"false\"\n"+
			"      timeout: \"1s\"\n"+
			"      interval: \"100ms\"\n"+
			"  - name: good\n"+
			"    command: \"sleep 30\"\n"+
			"    health:\n"+
			"      command: \"true\"\n"+
			"      timeout: \"10s\"\n"+
			"      interval: \"100ms\"\n")

	up, out := startUp(t, bin, dir, cfgPath, ws)

	// Despite the optional service failing, the environment comes up (state is
	// written once the required service is healthy).
	if !waitFor(15*time.Second, func() bool {
		st, err := state.Load(ws)
		return err == nil && len(st.Services) >= 1
	}) {
		t.Fatalf("environment never came up despite only an optional failure\n--- output ---\n%s", out.String())
	}
	if !strings.Contains(out.String(), "optional service") {
		t.Fatalf("expected a warning about the optional service:\n%s", out.String())
	}

	// Clean teardown.
	if err := up.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal up: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- up.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("up did not shut down cleanly: %v\n--- output ---\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("up did not exit within 15s of SIGINT\n--- output ---\n%s", out.String())
	}
}

// TestUpPositionalSelectsServiceAndDeps exercises the real CLI wiring for
// `tarjan up <service>`: naming one service starts it and its dependencies
// (pulled in transitively), while an unrelated service stays down. This is the
// end-to-end proof that positional args reach the selection, not just a flag.
func TestUpPositionalSelectsServiceAndDeps(t *testing.T) {
	skipUnlessE2E(t)

	bin := buildBinary(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")

	// svc-b dependsOn svc-a; svc-c is unrelated. `up svc-b` must bring up
	// svc-a + svc-b and leave svc-c down.
	svc := func(name, deps string) string {
		s := "  - name: " + name + "\n" +
			"    command: \"sleep 30\"\n"
		if deps != "" {
			s += "    dependsOn: [" + deps + "]\n"
		}
		return s + "    health:\n" +
			"      command: \"true\"\n" +
			"      timeout: \"20s\"\n" +
			"      interval: \"200ms\"\n"
	}
	cfgPath := writeConfig(t, dir, "e2e-positional",
		svc("svc-a", "")+svc("svc-b", "svc-a")+svc("svc-c", ""))

	up := exec.Command(bin, "-c", cfgPath, "up", "svc-b", "-w", ws)
	up.Dir = dir
	up.Env = append(os.Environ(), "TARJAN_NO_UPDATE_CHECK=1")
	out := &syncBuffer{}
	up.Stdout, up.Stderr = out, out
	if err := up.Start(); err != nil {
		t.Fatalf("start up: %v", err)
	}
	t.Cleanup(func() {
		if up.Process != nil {
			_ = up.Process.Kill()
			_, _ = up.Process.Wait()
		}
	})

	// State lands once the selected services are healthy.
	if !waitFor(15*time.Second, func() bool {
		st, err := state.Load(ws)
		return err == nil && len(st.Services) == 2
	}) {
		t.Fatalf("expected 2 services up (svc-a + svc-b)\n--- output ---\n%s", out.String())
	}
	st, err := state.Load(ws)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	names := map[string]bool{}
	for _, s := range st.Services {
		names[s.Name] = true
	}
	if !names["svc-a"] || !names["svc-b"] {
		t.Fatalf("positional up svc-b did not start svc-b + its dep svc-a: %v", names)
	}
	if names["svc-c"] {
		t.Fatalf("unrelated svc-c should not have started: %v", names)
	}

	// Clean teardown.
	if err := up.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal up: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- up.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("up did not shut down cleanly: %v\n--- output ---\n%s", err, out.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("up did not exit within 15s of SIGINT\n--- output ---\n%s", out.String())
	}
}
