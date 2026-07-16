package repocfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

// writeRepoConfig creates <ws>/<repo>/.tarjan/<file> with the given content
// and returns the workspace dir.
func writeRepoConfig(t *testing.T, ws, repo, file, content string) {
	t.Helper()
	dir := filepath.Join(ws, repo, config.RepoConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func baseConfig(t *testing.T) *config.Config {
	t.Helper()
	c := &config.Config{Name: "prod", WorkspaceRoot: t.TempDir()}
	if err := c.Finalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFindPrefersStarThenYAML(t *testing.T) {
	ws := t.TempDir()
	if got := Find(filepath.Join(ws, "api")); got != "" {
		t.Fatalf("Find on missing dir = %q, want empty", got)
	}
	writeRepoConfig(t, ws, "api", "tarjan.yml", "name: api\n")
	writeRepoConfig(t, ws, "api", "tarjan.yaml", "name: api\n")
	got := Find(filepath.Join(ws, "api"))
	if filepath.Base(got) != "tarjan.yaml" {
		t.Fatalf("Find = %q, want tarjan.yaml preferred over tarjan.yml", got)
	}
	writeRepoConfig(t, ws, "api", "tarjan.star", `tarjan = config(name = "api")`)
	got = Find(filepath.Join(ws, "api"))
	if filepath.Base(got) != "tarjan.star" {
		t.Fatalf("Find = %q, want tarjan.star preferred", got)
	}
}

// TestApplyRecordsToolVersionConflict guards the B5 fix: when a repo re-declares
// an already-required tool with a different minVersion, the existing entry wins
// (no duplicate added) but the dropped requirement is surfaced in SkippedTools
// instead of vanishing silently.
func TestApplyRecordsToolVersionConflict(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
requires:
  - name: node
    minVersion: "20"
services:
  - name: server
    command: run
`)
	base := baseConfig(t)
	base.Requires = []config.Tool{{Name: "node", MinVersion: "18"}}
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	base.Repos = []config.Repo{repo}

	sum, err := Apply(base, ws, []config.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	// node must not be duplicated into the requirements.
	nodeCount := 0
	for _, tool := range base.Requires {
		if tool.Name == "node" {
			nodeCount++
		}
	}
	if nodeCount != 1 {
		t.Fatalf("node appears %d times in requires, want 1 (no duplicate)", nodeCount)
	}
	// The conflict must be recorded, naming the wanted and kept versions.
	got := sum.Merged[0].SkippedTools
	if len(got) != 1 || !strings.Contains(got[0], "node") ||
		!strings.Contains(got[0], "20") || !strings.Contains(got[0], "18") {
		t.Fatalf("SkippedTools = %v, want it to record node 20 vs kept 18", got)
	}
}

func TestApplyMergesServicesAndTools(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
requires:
  - name: docker
  - name: uv
services:
  - name: db
    docker:
      image: postgres:16
  - name: server
    command: run server
    dependsOn: [db]
  - name: builder
    workdir: cmd/api
    docker:
      build:
        context: .
`)
	base := baseConfig(t)
	base.Requires = []config.Tool{{Name: "docker", MinVersion: "24"}}
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	base.Repos = []config.Repo{repo}

	sum, err := Apply(base, ws, []config.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Merged) != 1 || len(sum.Merged[0].Services) != 3 {
		t.Fatalf("merged = %+v, want 1 repo with 3 services", sum.Merged)
	}
	// Only the tool the base config lacks is added.
	if tools := sum.Tools(); len(tools) != 1 || tools[0].Name != "uv" {
		t.Fatalf("added tools = %+v, want just uv", tools)
	}
	byName := map[string]config.Service{}
	for _, s := range base.Services {
		byName[s.Name] = s
	}
	// An empty workdir becomes the repo checkout; a set one is prefixed.
	if got := byName["server"].Workdir; got != "api" {
		t.Fatalf("server workdir = %q, want %q", got, "api")
	}
	if got := byName["builder"].Workdir; got != filepath.Join("api", "cmd/api") {
		t.Fatalf("builder workdir = %q, want under repo", got)
	}
	// Docker build contexts are rebased onto the repo path too.
	if got := byName["builder"].Docker.Build.Context; got != "api" {
		t.Fatalf("build context = %q, want %q", got, "api")
	}
	if _, err := base.StartOrder(); err != nil {
		t.Fatalf("merged config start order: %v", err)
	}
}

func TestApplyParentDefinitionWins(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
services:
  - name: db
    docker:
      image: postgres:15
  - name: server
    command: run server
`)
	base := baseConfig(t)
	base.Services = []config.Service{{Name: "db", Docker: &config.DockerSpec{Image: "postgres:16"}}}
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}

	sum, err := Apply(base, ws, []config.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Merged[0].Skipped; len(got) != 1 || got[0] != "db" {
		t.Fatalf("skipped = %v, want [db]", got)
	}
	for _, s := range base.Services {
		if s.Name == "db" && s.Docker.Image != "postgres:16" {
			t.Fatalf("db image = %q, parent definition should win", s.Docker.Image)
		}
	}
}

func TestApplyRebasesEnvFiles(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
envFile: [shared.env]
services:
  - name: server
    command: run server
    envFile: [.env.local]
`)
	repoDir := filepath.Join(ws, "api")
	for _, f := range []string{"shared.env", ".env.local"} {
		if err := os.WriteFile(filepath.Join(repoDir, f), []byte("A=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := baseConfig(t)
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	if _, err := Apply(base, ws, []config.Repo{repo}); err != nil {
		t.Fatal(err)
	}
	svc := base.Services[0]
	want := []string{filepath.Join(repoDir, "shared.env"), filepath.Join(repoDir, ".env.local")}
	if len(svc.EnvFile) != 2 || svc.EnvFile[0] != want[0] || svc.EnvFile[1] != want[1] {
		t.Fatalf("envFile = %v, want %v", svc.EnvFile, want)
	}
	// The base config resolves them even though its own dir is elsewhere.
	if _, err := base.ServiceEnv(svc); err != nil {
		t.Fatalf("ServiceEnv on merged service: %v", err)
	}
}

func TestApplyFragmentMayDependOnParentServices(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
services:
  - name: server
    command: run server
    dependsOn: [postgres]
`)
	base := baseConfig(t)
	base.Services = []config.Service{{Name: "postgres", Docker: &config.DockerSpec{Image: "postgres:16"}}}
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	if _, err := Apply(base, ws, []config.Repo{repo}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyDanglingDependencyFailsAfterMerge(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.yaml", `
services:
  - name: server
    command: run server
    dependsOn: [nope]
`)
	base := baseConfig(t)
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	if _, err := Apply(base, ws, []config.Repo{repo}); err == nil {
		t.Fatal("want validation error for dangling dependsOn after merge")
	}
}

func TestApplyStarFragment(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "api", "tarjan.star", `
tarjan = config(
    name = "api",
    services = [service(name = "server", command = "run server", workdir = "srv")],
)
`)
	base := baseConfig(t)
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git"}
	sum, err := Apply(base, ws, []config.Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Merged) != 1 || len(sum.Merged[0].Services) != 1 {
		t.Fatalf("merged = %+v, want the star fragment's service", sum.Merged)
	}
	if got := base.Services[0].Workdir; got != filepath.Join("api", "srv") {
		t.Fatalf("workdir = %q, want under repo", got)
	}
}

func TestApplyRepoWithoutConfigIsSkipped(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := baseConfig(t)
	sum, err := Apply(base, ws, []config.Repo{{Name: "plain", URL: "https://example.com/plain.git"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Merged) != 0 {
		t.Fatalf("merged = %+v, want none", sum.Merged)
	}
}

func TestApplyRespectsRepoDirOverride(t *testing.T) {
	ws := t.TempDir()
	writeRepoConfig(t, ws, "checkout/api", "tarjan.yaml", `
services:
  - name: server
    command: run server
`)
	base := baseConfig(t)
	repo := config.Repo{Name: "api", URL: "https://example.com/api.git", Dir: "checkout/api"}
	if _, err := Apply(base, ws, []config.Repo{repo}); err != nil {
		t.Fatal(err)
	}
	if got := base.Services[0].Workdir; got != filepath.Join("checkout", "api") {
		t.Fatalf("workdir = %q, want the overridden checkout path", got)
	}
}
