package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// TestServiceEnvPrecedence pins the documented layering:
// process env < global envFile < service envFile < inline env.
func TestServiceEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "global.env"), "SHARED=global\nONLY_GLOBAL=g\n")
	writeFile(t, filepath.Join(dir, "svc.env"), "SHARED=service\nONLY_SVC=s\n")

	c := &Config{Name: "p", EnvFile: []string{"global.env"}}
	if err := c.Finalize(dir); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	spec := Service{
		Name:    "api",
		EnvFile: []string{"svc.env"},
		Env:     map[string]string{"SHARED": "inline", "ONLY_INLINE": "i"},
	}
	t.Setenv("SHARED", "process")
	t.Setenv("FROM_PROCESS", "p")

	env, err := c.ServiceEnv(spec)
	if err != nil {
		t.Fatalf("ServiceEnv: %v", err)
	}
	m := envMap(env)

	if m["SHARED"] != "inline" {
		t.Errorf("SHARED = %q, want inline (highest precedence)", m["SHARED"])
	}
	for k, want := range map[string]string{
		"ONLY_GLOBAL":  "g",
		"ONLY_SVC":     "s",
		"ONLY_INLINE":  "i",
		"FROM_PROCESS": "p",
	} {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}
}

func TestServiceEnvAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(t.TempDir(), "abs.env")
	writeFile(t, abs, "ABS=1\n")

	c := &Config{Name: "p"}
	if err := c.Finalize(dir); err != nil {
		t.Fatal(err)
	}
	env, err := c.ServiceEnv(Service{Name: "api", EnvFile: []string{abs}})
	if err != nil {
		t.Fatalf("ServiceEnv: %v", err)
	}
	if envMap(env)["ABS"] != "1" {
		t.Error("absolute env-file path was not loaded")
	}
}

func TestServiceEnvMissingFileErrors(t *testing.T) {
	c := &Config{Name: "p"}
	if err := c.Finalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ServiceEnv(Service{Name: "api", EnvFile: []string{"nope.env"}}); err == nil {
		t.Fatal("ServiceEnv with a missing env file = nil error, want error")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tarjan.yaml")
	writeFile(t, path, "name: demo\nservices:\n  - name: api\n    command: run\n")

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "demo" {
		t.Errorf("Name = %q, want demo", c.Name)
	}
	if c.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", c.Dir(), dir)
	}
	if c.InPlaceDir() != "" {
		t.Errorf("InPlaceDir() = %q, want empty for a top-level config", c.InPlaceDir())
	}
}

func TestLoadInvalidConfigErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tarjan.yaml")
	// A service with neither command nor docker fails validation.
	writeFile(t, path, "name: demo\nservices:\n  - name: broken\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load of an invalid config = nil error, want validation error")
	}
}

// TestResolveDirInPlace verifies a config inside a repo's .tarjan directory is
// resolved against the repo root and flagged in-place.
func TestResolveDirInPlace(t *testing.T) {
	repo := t.TempDir()
	cfgDir := filepath.Join(repo, RepoConfigDir)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "tarjan.yaml")

	dir, inPlace, err := ResolveDir(path)
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if dir != repo || inPlace != repo {
		t.Fatalf("ResolveDir = (%q, %q), want both %q", dir, inPlace, repo)
	}

	writeFile(t, path, "name: api\nservices:\n  - name: api\n    command: run\n")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.InPlaceDir() != repo {
		t.Errorf("InPlaceDir() = %q, want %q", c.InPlaceDir(), repo)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := expandPath("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("expandPath(~/x) = %q, want %q", got, filepath.Join(home, "x"))
	}
	t.Setenv("TARJAN_TEST_ROOT", "/opt/root")
	if got := expandPath("$TARJAN_TEST_ROOT/ws"); got != "/opt/root/ws" {
		t.Errorf("expandPath($VAR/ws) = %q, want /opt/root/ws", got)
	}
	if got := expandPath("/plain/path"); got != "/plain/path" {
		t.Errorf("expandPath(plain) = %q, want unchanged", got)
	}
}

// TestNormalizeDefaultsName defaults the product name to the config dir's base.
func TestNormalizeDefaultsName(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "myproduct")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Config{}
	if err := c.Finalize(dir); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if c.Name != "myproduct" {
		t.Errorf("defaulted Name = %q, want myproduct", c.Name)
	}
	if c.WorkspaceRoot == "" {
		t.Error("WorkspaceRoot should default to a non-empty path")
	}
}
