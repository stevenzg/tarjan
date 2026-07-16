package cmd

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stevenzg/tarjan/internal/state"
)

// deadPID returns the pid of a process that has already exited (and been
// reaped), so it is guaranteed not to be alive.
func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("sh", "-c", "exit 0")
	if err := c.Start(); err != nil {
		t.Skipf("cannot spawn a helper process: %v", err)
	}
	pid := c.Process.Pid
	_ = c.Wait()
	return pid
}

func TestPreflightPreviousRun(t *testing.T) {
	t.Run("no prior state is fine", func(t *testing.T) {
		ws := t.TempDir()
		if err := preflightPreviousRun(ws); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("stale state with no live process is cleared", func(t *testing.T) {
		ws := t.TempDir()
		save(t, ws, state.Service{Name: "api", PID: deadPID(t)})
		if err := preflightPreviousRun(ws); err != nil {
			t.Fatalf("want nil (stale), got %v", err)
		}
		if _, err := os.Stat(state.Path(ws)); !os.IsNotExist(err) {
			t.Fatalf("stale state file should have been removed, stat err = %v", err)
		}
	})

	t.Run("a live orphan process refuses the run", func(t *testing.T) {
		ws := t.TempDir()
		// os.Getpid() is this test process — definitively alive — standing in
		// for an orphaned service left holding a port.
		save(t, ws, state.Service{Name: "api", PID: os.Getpid()})
		if err := preflightPreviousRun(ws); err == nil {
			t.Fatal("want a refusal for a live process, got nil")
		}
		if _, err := os.Stat(state.Path(ws)); err != nil {
			t.Fatalf("state file should be left intact for `tarjan down`, stat err = %v", err)
		}
	})
}

func save(t *testing.T, ws string, svcs ...state.Service) {
	t.Helper()
	if err := state.Save(ws, &state.State{Name: "test", Workspace: ws, Services: svcs}); err != nil {
		t.Fatalf("save state: %v", err)
	}
}
