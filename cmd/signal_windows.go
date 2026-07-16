//go:build windows

package cmd

import (
	"os"

	"golang.org/x/sys/windows"
)

// stopPID kills the process by pid (Windows lacks process-group signals here).
func stopPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// killPID force-kills the process. On Windows stopPID is already a hard kill, so
// this is the same operation — it exists to satisfy the cross-platform escalation
// path in `down` (where a process that survived stopPID is force-killed).
func killPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// pidAlive reports whether a process with pid currently exists. Windows' Go
// runtime does not support Signal(0), so liveness is probed by opening the
// process and reading its exit code: a live process reports STILL_ACTIVE.
// (A process that genuinely exited with code 259 would look alive, but that is
// acceptable for this best-effort orphan check — the primary preflight signal
// is the control server's TCP probe.)
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE
	return code == stillActive
}
