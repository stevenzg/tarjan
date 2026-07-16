//go:build windows

package runner

import "os/exec"

// setProcAttr is a no-op on Windows; process-group semantics differ.
func setProcAttr(cmd *exec.Cmd) {}

// terminate kills the process (Windows has no graceful group signal here).
func terminate(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// kill force-kills the process.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
