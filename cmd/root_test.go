package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// setConfigFlag sets the shared --config flag for a test and restores it after.
func setConfigFlag(t *testing.T, v string) {
	t.Helper()
	prev := configFlag
	configFlag = v
	t.Cleanup(func() { configFlag = prev })
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveConfigPathFromFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom.yaml")
	mustWrite(t, p, "name: x\n")
	setConfigFlag(t, p)

	got, err := resolveConfigPath()
	if err != nil {
		t.Fatalf("resolveConfigPath: %v", err)
	}
	if got != p {
		t.Fatalf("path = %q, want %q", got, p)
	}
}

func TestResolveConfigPathFlagMissing(t *testing.T) {
	setConfigFlag(t, filepath.Join(t.TempDir(), "nope.yaml"))
	if _, err := resolveConfigPath(); err == nil {
		t.Fatal("missing --config file should error")
	}
}

func TestResolveConfigPathCandidate(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "tarjan.yaml"), "name: x\n")
	t.Chdir(dir)
	setConfigFlag(t, "")

	got, err := resolveConfigPath()
	if err != nil {
		t.Fatalf("resolveConfigPath: %v", err)
	}
	if got != "tarjan.yaml" {
		t.Fatalf("path = %q, want tarjan.yaml", got)
	}
}

func TestResolveConfigPathNone(t *testing.T) {
	t.Chdir(t.TempDir())
	setConfigFlag(t, "")
	if _, err := resolveConfigPath(); err == nil {
		t.Fatal("no config present should error")
	}
}

func TestLoadConfigYAML(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "tarjan.yaml"),
		"name: demo\nservices:\n  - name: api\n    command: run\n")
	t.Chdir(dir)
	setConfigFlag(t, "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Name != "demo" {
		t.Fatalf("Name = %q, want demo", cfg.Name)
	}
}
