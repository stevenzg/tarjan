package starcfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func load(t *testing.T, src string) (*config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tarjan.star")
	if werr := os.WriteFile(path, []byte(src), 0o644); werr != nil {
		t.Fatal(werr)
	}
	return Load(path)
}

func TestLoadToolProviders(t *testing.T) {
	src := `
tarjan = config(
    name = "demo",
    requires = [
        tool(name = "dotnet", min_version = "10", mise = "dotnet@10"),
        tool(name = "psql", package = {"apt": "postgresql-client", "brew": "libpq"}),
        tool(name = "redis-cli", package = "redis-tools"),
    ],
    services = [service(name = "a", command = "true")],
)
`
	cfg, err := load(t, src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Requires) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(cfg.Requires))
	}
	if cfg.Requires[0].Mise != "dotnet@10" {
		t.Fatalf("dotnet mise = %q", cfg.Requires[0].Mise)
	}
	if got := cfg.Requires[1].Package.Name("apt"); got != "postgresql-client" {
		t.Fatalf("psql apt package = %q", got)
	}
	if got := cfg.Requires[1].Package.Name("brew"); got != "libpq" {
		t.Fatalf("psql brew package = %q", got)
	}
	if got := cfg.Requires[2].Package.Name("apt"); got != "redis-tools" {
		t.Fatalf("scalar package = %q", got)
	}
}

func TestLoadGeneratesServices(t *testing.T) {
	src := `
db = service(
    name = "db",
    docker = docker(image = "postgres:16", ports = ["5432:5432"]),
    health = health(tcp = "localhost:5432"),
)
services = [db]
for i, n in enumerate(["a", "b", "c"]):
    services.append(service(
        name = n,
        command = "go run ./" + n,
        env = {"PORT": str(8000 + i)},
        depends_on = ["db"],
    ))
tarjan = config(
    name = "demo",
    repos = [repo(name = "api", url = "https://example.com/api.git")],
    services = services,
    workspace = workspace(vscode = True),
)
`
	path := filepath.Join(t.TempDir(), "tarjan.star")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Name != "demo" {
		t.Fatalf("name = %q", cfg.Name)
	}
	if len(cfg.Services) != 4 {
		t.Fatalf("expected 4 services (db + 3), got %d", len(cfg.Services))
	}
	if !cfg.Workspace.VSCode {
		t.Fatal("workspace.vscode not set")
	}

	byName := map[string]bool{}
	for _, s := range cfg.Services {
		byName[s.Name] = true
	}
	for _, want := range []string{"db", "a", "b", "c"} {
		if !byName[want] {
			t.Fatalf("missing generated service %q", want)
		}
	}

	// The loop's computed values landed correctly.
	for _, s := range cfg.Services {
		if s.Name == "b" {
			if s.Env["PORT"] != "8001" {
				t.Fatalf("service b PORT = %q, want 8001", s.Env["PORT"])
			}
			if len(s.DependsOn) != 1 || s.DependsOn[0] != "db" {
				t.Fatalf("service b dependsOn = %v", s.DependsOn)
			}
		}
		if s.Name == "db" && (s.Docker == nil || s.Docker.Image != "postgres:16") {
			t.Fatalf("db docker spec wrong: %+v", s.Docker)
		}
	}

	// Finalize ran (validation + start order).
	order, err := cfg.StartOrder()
	if err != nil {
		t.Fatalf("StartOrder: %v", err)
	}
	if order[0].Name != "db" {
		t.Fatalf("db should start first, got %v", order[0].Name)
	}
}

func TestLoadRequiresTarjanGlobal(t *testing.T) {
	if _, err := load(t, "x = 1\n"); err == nil {
		t.Fatal("expected error when `tarjan` is not defined")
	}
}

func TestLoadValidationError(t *testing.T) {
	// A dependency cycle must be rejected by Finalize's validation.
	src := `
tarjan = config(name = "x", services = [
    service(name = "a", command = "true", depends_on = ["b"]),
    service(name = "b", command = "true", depends_on = ["a"]),
])
`
	if _, err := load(t, src); err == nil {
		t.Fatal("expected validation error for dependency cycle")
	}
}

func TestLoadRemotes(t *testing.T) {
	src := `
tarjan = config(
    name = "demo",
    remotes = {
        "devbox": remote(host = "dev.example.com", user = "steven", port = 2222,
                         identity_file = "~/.ssh/id", options = ["StrictHostKeyChecking=accept-new"],
                         forward = False),
    },
    services = [
        service(name = "api", command = "go run .", workdir = "api", remote = "devbox",
                health = health(tcp = "localhost:8080")),
    ],
)
`
	cfg, err := load(t, src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rem, ok := cfg.Remotes["devbox"]
	if !ok {
		t.Fatal("remote devbox not parsed")
	}
	if rem.Host != "dev.example.com" || rem.User != "steven" || rem.Port != 2222 {
		t.Errorf("remote fields = %+v", rem)
	}
	if rem.IdentityFile != "~/.ssh/id" || len(rem.Options) != 1 {
		t.Errorf("identity/options not parsed: %+v", rem)
	}
	if rem.ForwardEnabled() {
		t.Error("forward = False should disable forwarding")
	}
	if cfg.Services[0].Remote != "devbox" {
		t.Errorf("service.remote = %q, want devbox", cfg.Services[0].Remote)
	}
}
