package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeInPlace writes a config into <repo>/.tarjan/tarjan.yaml and returns
// both paths.
func writeInPlace(t *testing.T, content string) (repoDir, cfgPath string) {
	t.Helper()
	repoDir = filepath.Join(t.TempDir(), "myrepo")
	dir := filepath.Join(repoDir, RepoConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "tarjan.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir, cfgPath
}

func TestLoadFromRepoConfigDir(t *testing.T) {
	repoDir, cfgPath := writeInPlace(t, `
services:
  - name: server
    command: run server
`)
	c, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.InPlaceDir() != repoDir {
		t.Fatalf("InPlaceDir = %q, want %q", c.InPlaceDir(), repoDir)
	}
	// Relative paths (and the default name) resolve against the repo root,
	// not the .tarjan folder.
	if c.Dir() != repoDir {
		t.Fatalf("Dir = %q, want %q", c.Dir(), repoDir)
	}
	if c.Name != "myrepo" {
		t.Fatalf("Name = %q, want the repo folder name", c.Name)
	}
}

func TestLoadTopLevelHasNoInPlaceDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tarjan.yaml")
	if err := os.WriteFile(path, []byte("name: prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.InPlaceDir() != "" {
		t.Fatalf("InPlaceDir = %q, want empty for a top-level config", c.InPlaceDir())
	}
}

func TestLoadFragmentSkipsValidation(t *testing.T) {
	_, cfgPath := writeInPlace(t, `
services:
  - name: server
    command: run server
    dependsOn: [defined-by-parent]
`)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load should reject a dangling dependsOn")
	}
	c, err := LoadFragment(cfgPath)
	if err != nil {
		t.Fatalf("LoadFragment: %v", err)
	}
	if len(c.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(c.Services))
	}
}
