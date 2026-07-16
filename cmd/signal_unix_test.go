//go:build !windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Fatal("the current process should be reported alive")
	}
	if pidAlive(0) || pidAlive(-1) {
		t.Fatal("non-positive pids are never alive")
	}
	if pidAlive(deadPID(t)) {
		t.Fatal("a reaped process should not be reported alive")
	}
}

func TestStopPID(t *testing.T) {
	c := exec.Command("sleep", "30")
	// Start the helper as its own process-group leader, matching how `tarjan up`
	// starts services (Setpgid) — stopPID signals the whole group by its leader
	// PID and no longer falls back to the bare PID.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Skipf("cannot spawn a helper process: %v", err)
	}
	pid := c.Process.Pid

	stopPID(pid)

	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.Process.Kill()
		t.Fatal("stopPID did not terminate the process")
	}
	if pidAlive(pid) {
		t.Fatal("process still alive after stopPID")
	}
}
