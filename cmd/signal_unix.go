//go:build !windows

package cmd

import "syscall"

// stopPID terminates the process group started by `up` (negative pid). tarjan
// starts every child as its own group leader (Setpgid), so the recorded PID is
// the process-group ID. If signalling the group fails with ESRCH the group is
// already gone — we deliberately do NOT fall back to killing the bare PID,
// because after PID reuse that number may belong to an unrelated process.
func stopPID(pid int) {
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if err == nil || err == syscall.ESRCH {
		return
	}
	// A non-ESRCH error (e.g. EPERM) means the group exists but we couldn't
	// signal it as a group; try the leader directly as a last resort.
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// killPID force-kills the process group started by `up` (negative pid), the
// SIGKILL escalation when a SIGTERM was ignored within the grace window. As with
// stopPID it does not fall back to the bare PID on ESRCH, to avoid killing an
// unrelated process after PID reuse.
func killPID(pid int) {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil || err == syscall.ESRCH {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// pidAlive reports whether a process with pid currently exists. Signal 0 does
// not touch the process, only probes it: no error (or EPERM, meaning it exists
// but we may not signal it) means alive; ESRCH means gone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
