package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stevenzg/tarjan/internal/state"
)

type fakeEnv struct {
	restarted []string
	reloaded  int
}

func (f *fakeEnv) Reload() error {
	f.reloaded++
	return nil
}

func (f *fakeEnv) RestartService(name string) error {
	if name == "bad" {
		return fmt.Errorf("cannot restart %q", name)
	}
	f.restarted = append(f.restarted, name)
	return nil
}

func (f *fakeEnv) Status() []state.Service {
	return []state.Service{{Name: "api", Job: false}}
}

func (f *fakeEnv) IsReady(name string) bool { return name == "api" }

func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".tarjan"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestControlRoundTrip(t *testing.T) {
	dir := newWorkspace(t)
	env := &fakeEnv{}
	srv, err := Serve(dir, env)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer srv.Close()

	if err := Restart(dir, "api"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(env.restarted) != 1 || env.restarted[0] != "api" {
		t.Fatalf("RestartService not invoked: %v", env.restarted)
	}

	// An error from the env surfaces to the client.
	if err := Restart(dir, "bad"); err == nil {
		t.Fatal("expected error restarting 'bad'")
	}

	if err := Reload(dir); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if env.reloaded != 1 {
		t.Fatalf("Reload not invoked: %d", env.reloaded)
	}

	statuses, err := Statuses(dir)
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Name != "api" || !statuses[0].Ready {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}
}

func TestControlNoServer(t *testing.T) {
	dir := newWorkspace(t)
	if err := Restart(dir, "api"); !errors.Is(err, ErrNoServer) {
		t.Fatalf("expected ErrNoServer, got %v", err)
	}
}
