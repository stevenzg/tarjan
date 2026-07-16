// Package shellx centralises how a command string is handed to the OS shell,
// so every part of tarjan runs user commands the same way.
package shellx

import "runtime"

// Command returns the shell and arguments to execute a command string on the
// current OS: "sh -c" on Unix, "cmd /c" on Windows.
func Command(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", command}
	}
	return "sh", []string{"-c", command}
}
