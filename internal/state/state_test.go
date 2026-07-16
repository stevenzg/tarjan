package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	ws := t.TempDir()
	want := &State{
		Name:      "prod",
		Workspace: ws,
		StartedAt: time.Now().Round(time.Second),
		Services: []Service{
			{Name: "api", PID: 1234},
			{Name: "web", Container: "tarjan-prod-web", Docker: true},
			{Name: "cloud", External: true},
			{Name: "migrate", Job: true},
		},
	}
	if err := Save(ws, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(ws)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != want.Name || got.Workspace != want.Workspace {
		t.Errorf("meta mismatch: got %+v", got)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if len(got.Services) != len(want.Services) {
		t.Fatalf("services len = %d, want %d", len(got.Services), len(want.Services))
	}
	for i, s := range got.Services {
		if s != want.Services[i] {
			t.Errorf("service[%d] = %+v, want %+v", i, s, want.Services[i])
		}
	}
}

func TestPath(t *testing.T) {
	got := Path(filepath.FromSlash("/tmp/ws"))
	want := filepath.Join(filepath.FromSlash("/tmp/ws"), ".tarjan", "state.json")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestSaveCreatesTarjanDir(t *testing.T) {
	ws := t.TempDir()
	if err := Save(ws, &State{Name: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(filepath.Join(ws, ".tarjan"))
	if err != nil || !fi.IsDir() {
		t.Fatalf(".tarjan dir not created: %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("Load on missing state = nil error, want error")
	}
}

func TestLoadCorrupt(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".tarjan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(ws), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ws); err == nil {
		t.Fatal("Load on corrupt JSON = nil error, want error")
	}
}

// TestSaveAtomicNoLeftoverTemp checks Save leaves only the final state.json in
// .tarjan — the temp file it writes must be renamed away, never left behind.
func TestSaveAtomicNoLeftoverTemp(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Save(ws, &State{Name: "x"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(ws, ".tarjan"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf(".tarjan contents = %v, want just [state.json]", names)
	}
}

func TestRemove(t *testing.T) {
	ws := t.TempDir()
	if err := Save(ws, &State{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	Remove(ws)
	if _, err := os.Stat(Path(ws)); !os.IsNotExist(err) {
		t.Fatalf("state file still present after Remove: %v", err)
	}
	// Remove is best-effort: removing an already-absent file must not panic.
	Remove(ws)
}
