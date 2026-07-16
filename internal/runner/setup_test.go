package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

// newSetupFixture builds a runner over a fresh workspace with the service's
// workdir and the .tarjan marker directory in place, and returns a supervisor
// for the spec plus the workspace and workdir paths. Setup and setupCheck run
// locally in the workdir.
func newSetupFixture(t *testing.T, spec config.Service) (sup *supervisor, ws, wd string) {
	t.Helper()
	ws = t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".tarjan"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd = ws
	if spec.Workdir != "" {
		wd = filepath.Join(ws, spec.Workdir)
		if err := os.MkdirAll(wd, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := New(&config.Config{Name: "test"}, ws)
	return newSupervisor(r, spec, 0), ws, wd
}

// ranCount counts how many times the setup commands ran, by counting the lines
// each run appends to the "ran" sentinel file in the workdir.
func ranCount(t *testing.T, wd string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wd, "ran"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

// A passing setupCheck records setup as done; a later run re-verifies the check
// and, since it still passes, skips setup instead of re-running it.
func TestRunSetupCheckPassMarksDoneAndSkips(t *testing.T) {
	spec := config.Service{
		Name: "app", Workdir: "app",
		Setup:      []string{"echo x >> ran && touch binary"},
		SetupCheck: "test -f binary",
	}
	s, _, wd := newSetupFixture(t, spec)

	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("first runSetup: %v", err)
	}
	if got := ranCount(t, wd); got != 1 {
		t.Fatalf("after first setup, ran=%d, want 1", got)
	}

	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("second runSetup: %v", err)
	}
	if got := ranCount(t, wd); got != 1 {
		t.Fatalf("passing check should skip re-run; ran=%d, want 1", got)
	}
}

// The marker can persist while the artifact it stood for is gone (the electron
// "package recorded, downloaded binary missing" case). A failing setupCheck
// then re-runs setup to self-heal instead of trusting the stale marker.
func TestRunSetupCheckFailReRunsSetup(t *testing.T) {
	spec := config.Service{
		Name: "app", Workdir: "app",
		Setup:      []string{"echo x >> ran && touch binary"},
		SetupCheck: "test -f binary",
	}
	s, _, wd := newSetupFixture(t, spec)

	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("first runSetup: %v", err)
	}
	if err := os.Remove(filepath.Join(wd, "binary")); err != nil {
		t.Fatal(err)
	}

	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("self-heal runSetup: %v", err)
	}
	if got := ranCount(t, wd); got != 2 {
		t.Fatalf("failing check should re-run setup; ran=%d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(wd, "binary")); err != nil {
		t.Fatalf("artifact should be restored: %v", err)
	}
}

// A setupCheck that fails right after setup means setup did not really produce
// what it should: runSetup errors and does NOT write the marker, so the broken
// workspace is not frozen as done — every run re-attempts setup.
func TestRunSetupCheckFailAfterSetupErrorsAndDoesNotMark(t *testing.T) {
	spec := config.Service{
		Name: "app", Workdir: "app",
		Setup:      []string{"echo x >> ran"}, // never creates `binary`
		SetupCheck: "test -f binary",
	}
	s, ws, wd := newSetupFixture(t, spec)

	if err := s.runSetup(context.Background()); err == nil {
		t.Fatal("expected error when setupCheck fails after setup")
	}
	marker := filepath.Join(ws, ".tarjan", "setup-app")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker must not be written on a failing check; stat err=%v", err)
	}
	if got := ranCount(t, wd); got != 1 {
		t.Fatalf("after first run, ran=%d, want 1", got)
	}

	// Not frozen: with no marker, a later run re-attempts setup.
	if err := s.runSetup(context.Background()); err == nil {
		t.Fatal("expected error on the retry too")
	}
	if got := ranCount(t, wd); got != 2 {
		t.Fatalf("broken setup should re-run; ran=%d, want 2", got)
	}
}

// Without a setupCheck, behaviour is unchanged: the marker alone gates setup, so
// a second run skips it (even if its artifacts were since removed).
func TestRunSetupWithoutCheckGatesOnMarker(t *testing.T) {
	spec := config.Service{
		Name: "app", Workdir: "app",
		Setup: []string{"echo x >> ran"},
	}
	s, _, wd := newSetupFixture(t, spec)

	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("first runSetup: %v", err)
	}
	if err := s.runSetup(context.Background()); err != nil {
		t.Fatalf("second runSetup: %v", err)
	}
	if got := ranCount(t, wd); got != 1 {
		t.Fatalf("marker should gate without a check; ran=%d, want 1", got)
	}
}
