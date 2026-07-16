package shellx

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	name, args := Command("echo hi")
	if runtime.GOOS == "windows" {
		if name != "cmd" || len(args) != 2 || args[0] != "/c" || args[1] != "echo hi" {
			t.Fatalf("windows: got name=%q args=%v", name, args)
		}
		return
	}
	if name != "sh" || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("unix: got name=%q args=%v", name, args)
	}
}

// TestCommandPreservesFullString guards the invariant that the whole command is
// handed to the shell as a single argument, so pipes, redirects and quoting are
// interpreted by the shell rather than split by tarjan.
func TestCommandPreservesFullString(t *testing.T) {
	cmd := `foo | bar > out && baz "a b"`
	_, args := Command(cmd)
	if got := args[len(args)-1]; got != cmd {
		t.Fatalf("command was mangled: got %q, want %q", got, cmd)
	}
}

// TestCommandRunsThroughShell exercises the returned invocation end-to-end: a
// shell feature (variable + pipe) must actually work when executed.
func TestCommandRunsThroughShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell syntax")
	}
	name, args := Command("printf 'a\\nb\\na\\n' | sort -u | tr -d '\\n'")
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "ab" {
		t.Fatalf("shell pipeline output = %q, want %q", got, "ab")
	}
}
