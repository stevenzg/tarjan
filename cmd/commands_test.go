package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/control"
)

// --- exec.go helpers ---

func TestFindService(t *testing.T) {
	cfg := &config.Config{Services: []config.Service{
		{Name: "api", Command: "run"},
		{Name: "web", Command: "run"},
	}}
	if s, ok := findService(cfg, "web"); !ok || s.Name != "web" {
		t.Fatalf("findService(web) = %+v, %v", s, ok)
	}
	if _, ok := findService(cfg, "ghost"); ok {
		t.Fatal("findService(ghost) should be false")
	}
}

func TestServiceNamesOf(t *testing.T) {
	cfg := &config.Config{Services: []config.Service{{Name: "a"}, {Name: "b"}}}
	got := serviceNamesOf(cfg)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("serviceNamesOf = %v", got)
	}
}

func TestDockerExec(t *testing.T) {
	withCmd := dockerExec("c1", []string{"psql", "-l"})
	want := []string{"docker", "exec", "-it", "c1", "psql", "-l"}
	if !equalStrings(withCmd, want) {
		t.Fatalf("dockerExec(cmd) = %v, want %v", withCmd, want)
	}
	// No command falls back to an interactive shell.
	noCmd := dockerExec("c1", nil)
	if len(noCmd) == 0 || noCmd[len(noCmd)-1] != "sh" {
		t.Fatalf("dockerExec(nil) = %v, want trailing sh", noCmd)
	}
}

func TestDefaultShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		if defaultShell() != "cmd" {
			t.Fatalf("windows shell = %q, want cmd", defaultShell())
		}
		return
	}
	t.Setenv("SHELL", "/bin/zsh")
	if got := defaultShell(); got != "/bin/zsh" {
		t.Fatalf("defaultShell with SHELL set = %q, want /bin/zsh", got)
	}
	t.Setenv("SHELL", "")
	if got := defaultShell(); got != "/bin/sh" {
		t.Fatalf("defaultShell without SHELL = %q, want /bin/sh", got)
	}
}

func TestRunProcess(t *testing.T) {
	if err := runProcess(nil, nil, ""); err == nil {
		t.Fatal("runProcess with no argv should error")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := runProcess([]string{"sh", "-c", "exit 0"}, nil, ""); err != nil {
		t.Fatalf("runProcess(true) = %v, want nil", err)
	}
	if err := runProcess([]string{"sh", "-c", "exit 3"}, nil, ""); err == nil {
		t.Fatal("runProcess with a failing command should error")
	}
}

// --- status.go labels ---

func TestCmdKindLabel(t *testing.T) {
	cases := map[string]control.ServiceStatus{
		"external (cloud/remote)": {External: true},
		"job":                     {Job: true},
		"docker box":              {Docker: true, Container: "box"},
		"pid 42":                  {PID: 42},
	}
	for want, s := range cases {
		if got := kindLabel(s); got != want {
			t.Errorf("kindLabel(%+v) = %q, want %q", s, got, want)
		}
	}
}

func TestReadyLabel(t *testing.T) {
	if readyLabel(true) != "ready" || readyLabel(false) != "starting" {
		t.Fatalf("readyLabel: %q / %q", readyLabel(true), readyLabel(false))
	}
}

// --- validate.go ---

func TestServiceNames(t *testing.T) {
	got := serviceNames([]config.Service{{Name: "x"}, {Name: "y"}})
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("serviceNames = %v", got)
	}
}

// --- logs.go ---

func TestListLogs(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"api.log", "web.log", "notes.txt"} {
		mustWrite(t, filepath.Join(dir, f), "")
	}
	if err := listLogs(dir); err != nil {
		t.Fatalf("listLogs = %v", err)
	}
	// Empty and missing directories warn but do not error.
	if err := listLogs(t.TempDir()); err != nil {
		t.Fatalf("listLogs(empty) = %v", err)
	}
	if err := listLogs(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("listLogs(missing) = %v", err)
	}
}

func TestDumpLog(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	mustWrite(t, p, "hello\nworld\n")
	if err := dumpLog(p); err != nil {
		t.Fatalf("dumpLog = %v", err)
	}
	if err := dumpLog(filepath.Join(dir, "missing.log")); err == nil {
		t.Fatal("dumpLog(missing) should error")
	}
}

// --- init.go ---

func TestRunInitWritesValidStarterYAML(t *testing.T) {
	t.Chdir(t.TempDir())
	prevStar, prevForce := initStar, initForce
	t.Cleanup(func() { initStar, initForce = prevStar, prevForce })
	initStar, initForce = false, false

	if err := runInit(initCmd, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// The scaffold must parse and validate — otherwise `tarjan init` ships a
	// broken starter config.
	if _, err := config.Load("tarjan.yaml"); err != nil {
		t.Fatalf("starter tarjan.yaml is not valid: %v", err)
	}
	// A second run without --force refuses to clobber.
	if err := runInit(initCmd, nil); err == nil {
		t.Fatal("runInit over an existing file should error without --force")
	}
	// With --force it overwrites.
	initForce = true
	if err := runInit(initCmd, nil); err != nil {
		t.Fatalf("runInit --force: %v", err)
	}
}

func TestRunInitStar(t *testing.T) {
	t.Chdir(t.TempDir())
	prevStar, prevForce := initStar, initForce
	t.Cleanup(func() { initStar, initForce = prevStar, prevForce })
	initStar, initForce = true, false

	if err := runInit(initCmd, nil); err != nil {
		t.Fatalf("runInit --star: %v", err)
	}
	if _, err := os.Stat("tarjan.star"); err != nil {
		t.Fatalf("tarjan.star not written: %v", err)
	}
}

// --- version.go ---

func TestVersionCmdRuns(t *testing.T) {
	// Smoke test: printing version metadata must not panic.
	versionCmd.Run(versionCmd, nil)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
