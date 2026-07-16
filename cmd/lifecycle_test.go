package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stevenzg/tarjan/internal/runner"
	"github.com/stevenzg/tarjan/internal/state"
)

// writeConfigInCWD writes a minimal, valid tarjan.yaml into a fresh temp dir,
// chdirs there, and points --config at it. workspaceRoot is set to a temp dir
// so nothing touches the real ~/tarjan. It returns the config dir.
func writeConfigInCWD(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	body := "name: demo\n" +
		"workspaceRoot: " + root + "\n" +
		"services:\n" +
		"  - name: api\n" +
		"    command: run\n" +
		extra
	mustWrite(t, filepath.Join(dir, "tarjan.yaml"), body)
	t.Chdir(dir)
	setConfigFlag(t, "")
	return dir
}

// --- down.go ---

func TestRunDownNoState(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := downWorkspace
	downWorkspace = ws
	t.Cleanup(func() { downWorkspace = prev })

	// No recorded state: warns but must not error.
	if err := runDown(downCmd, nil); err != nil {
		t.Fatalf("runDown(no state) = %v, want nil", err)
	}
}

func TestRunDownStopsAndClearsState(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := downWorkspace
	downWorkspace = ws
	t.Cleanup(func() { downWorkspace = prev })

	// A mix of a (dead) PID service and a docker service exercises both stop
	// paths. The docker calls no-op safely even when docker is absent.
	save(t, ws,
		state.Service{Name: "api", PID: deadPID(t)},
		state.Service{Name: "db", Docker: true, Container: "tarjan-test-db"},
	)

	if err := runDown(downCmd, nil); err != nil {
		t.Fatalf("runDown = %v, want nil", err)
	}
	// State must be cleared so the next `up` sees a clean slate.
	if _, err := state.Load(ws); err == nil {
		t.Fatal("state should have been removed after down")
	}
}

// --- status.go ---

func TestRunStatusNoEnvironment(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := statusWorkspace
	statusWorkspace = ws
	t.Cleanup(func() { statusWorkspace = prev })

	// No live control endpoint and no state file: warns, returns nil.
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus(no env) = %v, want nil", err)
	}
}

func TestRunStatusFromStateFile(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := statusWorkspace
	statusWorkspace = ws
	t.Cleanup(func() { statusWorkspace = prev })

	// Cover every kind label branch through the state-file path.
	save(t, ws,
		state.Service{Name: "api", PID: 4242},
		state.Service{Name: "db", Docker: true, Container: "box"},
		state.Service{Name: "seed", Job: true},
		state.Service{Name: "cloud", External: true},
	)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("runStatus(state) = %v, want nil", err)
	}
}

// --- validate.go ---

func TestValidateWholeConfig(t *testing.T) {
	writeConfigInCWD(t, "")
	prevOnly, prevProf, prevNoDeps := validateOnly, validateProfiles, validateNoDeps
	t.Cleanup(func() {
		validateOnly, validateProfiles, validateNoDeps = prevOnly, prevProf, prevNoDeps
	})
	validateOnly, validateProfiles, validateNoDeps = nil, nil, false

	if err := validateCmd.RunE(validateCmd, nil); err != nil {
		t.Fatalf("validate = %v, want nil", err)
	}
}

func TestValidateSelection(t *testing.T) {
	writeConfigInCWD(t, "  - name: web\n    command: run\n")
	prevOnly, prevProf, prevNoDeps := validateOnly, validateProfiles, validateNoDeps
	t.Cleanup(func() {
		validateOnly, validateProfiles, validateNoDeps = prevOnly, prevProf, prevNoDeps
	})
	// Preview a single-service selection with deps disabled.
	validateOnly, validateProfiles, validateNoDeps = []string{"web"}, nil, true

	if err := validateCmd.RunE(validateCmd, nil); err != nil {
		t.Fatalf("validate(selection) = %v, want nil", err)
	}
}

// --- workspace.go ---

func TestRunWorkspaceWritesCodeWorkspace(t *testing.T) {
	writeConfigInCWD(t, "repos:\n  - name: app\n    url: https://example.com/app.git\n")
	ws := t.TempDir()
	prevWs, prevOpen := wsWorkspace, wsOpen
	wsWorkspace, wsOpen = ws, false
	t.Cleanup(func() { wsWorkspace, wsOpen = prevWs, prevOpen })

	if err := runWorkspace(workspaceCmd, nil); err != nil {
		t.Fatalf("runWorkspace = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "demo.code-workspace")); err != nil {
		t.Fatalf("code-workspace not written: %v", err)
	}
}

// --- doctor.go ---

func TestDoctorNoRequires(t *testing.T) {
	writeConfigInCWD(t, "")
	prev := doctorInstall
	doctorInstall = false
	t.Cleanup(func() { doctorInstall = prev })

	// No `requires` declared: reports nothing to do, returns nil.
	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor(no requires) = %v, want nil", err)
	}
}

// --- reload.go / restart.go: control-plane commands error cleanly with no
// running environment (nothing to dial). ---

func TestReloadWithoutRunningEnvErrors(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := reloadWorkspace
	reloadWorkspace = ws
	t.Cleanup(func() { reloadWorkspace = prev })

	if err := reloadCmd.RunE(reloadCmd, nil); err == nil {
		t.Fatal("reload with no running environment should error")
	}
}

func TestRestartWithoutRunningEnvErrors(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prev := restartWorkspace
	restartWorkspace = ws
	t.Cleanup(func() { restartWorkspace = prev })

	if err := restartCmd.RunE(restartCmd, []string{"api"}); err == nil {
		t.Fatal("restart with no running environment should error")
	}
}

// --- logs.go: runLogs list + dump + missing-service paths ---

func TestRunLogs(t *testing.T) {
	writeConfigInCWD(t, "")
	ws := t.TempDir()
	prevWs, prevFollow := logsWorkspace, logsFollow
	logsWorkspace, logsFollow = ws, false
	t.Cleanup(func() { logsWorkspace, logsFollow = prevWs, prevFollow })

	logsDir := runner.LogsDir(ws)
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(logsDir, "api.log"), "line one\nline two\n")

	// No args: list the services that have logs.
	if err := runLogs(logsCmd, nil); err != nil {
		t.Fatalf("runLogs(list) = %v, want nil", err)
	}
	// A named service: dump its log.
	if err := runLogs(logsCmd, []string{"api"}); err != nil {
		t.Fatalf("runLogs(api) = %v, want nil", err)
	}
	// A service with no log file: a clear error.
	if err := runLogs(logsCmd, []string{"ghost"}); err == nil {
		t.Fatal("runLogs(ghost) should error when no log exists")
	}
}

// --- up.go helpers ---

func TestRunningServicesDetectsLiveOrphan(t *testing.T) {
	ws := t.TempDir()
	// No control server is running under ws, so detection falls back to live
	// local PIDs. os.Getpid() is definitively alive; a docker entry is ignored.
	st := &state.State{Services: []state.Service{
		{Name: "api", PID: os.Getpid()},
		{Name: "db", Docker: true, Container: "box"},
		{Name: "gone", PID: deadPID(t)},
	}}
	got := runningServices(ws, st)
	if len(got) != 1 || got[0] != "api" {
		t.Fatalf("runningServices = %v, want [api]", got)
	}
}

func TestDownHint(t *testing.T) {
	writeConfigInCWD(t, "")
	// A workspace that isn't the default resolves to an explicit -w hint.
	other := t.TempDir()
	if h := downHint(other); h == "" {
		t.Fatalf("downHint(%q) = %q, want a -w hint", other, h)
	}
}

func TestMergeRepoConfigsNoRepos(t *testing.T) {
	writeConfigInCWD(t, "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// With no repos there is nothing to merge and no tools to check.
	if err := mergeRepoConfigs(cfg, t.TempDir(), nil); err != nil {
		t.Fatalf("mergeRepoConfigs(no repos) = %v, want nil", err)
	}
}
