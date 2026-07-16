package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestCreateIsTimestamped(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "demo", WorkspaceRoot: root}
	stamp := time.Date(2026, 6, 30, 14, 15, 0, 0, time.UTC)

	dir, err := Create(cfg, stamp)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := filepath.Join(root, "20260630-141500"); dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tarjan")); err != nil {
		t.Fatalf(".tarjan not created: %v", err)
	}
}

func TestResolveMostRecent(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "demo", WorkspaceRoot: root}
	// Materialize records the dir as "last"; Resolve with no explicit arg must
	// return it.
	dir, err := Create(cfg, time.Date(2026, 6, 30, 14, 15, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := Resolve(cfg, "")
	if err != nil {
		t.Fatalf("Resolve(recent): %v", err)
	}
	if got != dir {
		t.Fatalf("Resolve = %q, want %q", got, dir)
	}
}

func TestResolveNoWorkspaceErrors(t *testing.T) {
	cfg := &config.Config{Name: "demo", WorkspaceRoot: filepath.Join(t.TempDir(), "empty")}
	if _, err := Resolve(cfg, ""); err == nil {
		t.Fatal("Resolve with no recorded workspace should error")
	}
}

func TestWriteVSCodeListsReposAsFolders(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "demo", WorkspaceRoot: root}
	repos := []config.Repo{
		{Name: "api", URL: "https://example.com/api.git"},
		{Name: "web", URL: "https://example.com/web.git"},
	}

	path, err := WriteVSCode(cfg, root, repos)
	if err != nil {
		t.Fatalf("WriteVSCode: %v", err)
	}
	if want := filepath.Join(root, "demo.code-workspace"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	var ws struct {
		Folders []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("workspace file is not valid JSON: %v", err)
	}
	if len(ws.Folders) != 2 || ws.Folders[0].Name != "api" || ws.Folders[1].Name != "web" {
		t.Fatalf("folders = %+v, want api + web", ws.Folders)
	}
}
