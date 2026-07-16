package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstallSpecScalar(t *testing.T) {
	var tool Tool
	if err := yaml.Unmarshal([]byte("name: node\ninstall: brew install node\n"), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := tool.Install.Command("darwin"); got != "brew install node" {
		t.Fatalf("scalar install darwin = %q", got)
	}
	if got := tool.Install.Command("linux"); got != "brew install node" {
		t.Fatalf("scalar install applies to all OSes, got %q", got)
	}
}

func TestInstallSpecPerOS(t *testing.T) {
	y := "name: pnpm\ninstall:\n  darwin: brew install pnpm\n  linux: npm i -g pnpm\n"
	var tool Tool
	if err := yaml.Unmarshal([]byte(y), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := tool.Install.Command("darwin"); got != "brew install pnpm" {
		t.Fatalf("darwin = %q", got)
	}
	if got := tool.Install.Command("linux"); got != "npm i -g pnpm" {
		t.Fatalf("linux = %q", got)
	}
	if got := tool.Install.Command("windows"); got != "" {
		t.Fatalf("windows (unset) = %q, want empty", got)
	}
	if tool.Install.IsZero() {
		t.Fatal("IsZero should be false when a map is set")
	}
}

func TestPackageSpecScalar(t *testing.T) {
	var tool Tool
	if err := yaml.Unmarshal([]byte("name: psql\npackage: postgresql-client\n"), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := tool.Package.Name("apt"); got != "postgresql-client" {
		t.Fatalf("scalar package apt = %q", got)
	}
	if got := tool.Package.Name("brew"); got != "postgresql-client" {
		t.Fatalf("scalar package applies to all managers, got %q", got)
	}
	if tool.Package.IsZero() {
		t.Fatal("IsZero should be false when a package is set")
	}
}

func TestPackageSpecPerManager(t *testing.T) {
	y := "name: psql\npackage:\n  apt: postgresql-client\n  brew: libpq\n"
	var tool Tool
	if err := yaml.Unmarshal([]byte(y), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := tool.Package.Name("apt"); got != "postgresql-client" {
		t.Fatalf("apt = %q", got)
	}
	if got := tool.Package.Name("brew"); got != "libpq" {
		t.Fatalf("brew = %q", got)
	}
	if got := tool.Package.Name("dnf"); got != "" {
		t.Fatalf("dnf (unset, no default) = %q, want empty", got)
	}
}

func TestMiseField(t *testing.T) {
	var tool Tool
	if err := yaml.Unmarshal([]byte("name: dotnet\nminVersion: \"10\"\nmise: dotnet@10\n"), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tool.Mise != "dotnet@10" {
		t.Fatalf("mise = %q", tool.Mise)
	}
}

func svc(name string, deps ...string) Service {
	return Service{Name: name, Command: "true", DependsOn: deps}
}

func TestSafeRelPath(t *testing.T) {
	ok := []string{"", "app", "nested/checkout", "a/b/c", "./app"}
	bad := []string{"..", "../x", "../../etc/passwd", "a/../../b", "/abs/path", "/etc/passwd"}
	for _, p := range ok {
		if !safeRelPath(p) {
			t.Errorf("safeRelPath(%q) = false, want true", p)
		}
	}
	for _, p := range bad {
		if safeRelPath(p) {
			t.Errorf("safeRelPath(%q) = true, want false (escapes workspace)", p)
		}
	}
}

func TestValidateRejectsRepoPathTraversal(t *testing.T) {
	c := &Config{Name: "x", Repos: []Repo{{Name: "app", URL: "https://example.com/app.git", Dir: "../../../tmp/evil"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted a repo dir that escapes the workspace")
	}
}

func TestValidateRejectsDuplicateRepoDir(t *testing.T) {
	c := &Config{Name: "x", Repos: []Repo{
		{Name: "a", URL: "https://example.com/a.git", Dir: "shared"},
		{Name: "b", URL: "https://example.com/b.git", Dir: "shared"},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted two repos cloning into the same dir")
	}
}

func TestValidateRejectsServiceWorkdirTraversal(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{{Name: "a", Command: "true", Workdir: "../escape"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted a service workdir that escapes the workspace")
	}
}

func TestStartOrderRespectsDependencies(t *testing.T) {
	c := &Config{Services: []Service{
		svc("web", "api"),
		svc("api", "db"),
		svc("db"),
	}}
	order, err := c.StartOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pos := map[string]int{}
	for i, s := range order {
		pos[s.Name] = i
	}
	if pos["db"] >= pos["api"] || pos["api"] >= pos["web"] {
		t.Fatalf("bad order: %v", names(order))
	}
}

func TestStartOrderDetectsCycle(t *testing.T) {
	c := &Config{Services: []Service{
		svc("a", "b"),
		svc("b", "a"),
	}}
	if _, err := c.StartOrder(); err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestValidateRejectsDanglingDependency(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{svc("a", "ghost")}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected dangling dependency error")
	}
}

func TestValidateRejectsServiceWithoutCommandOrDocker(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{{Name: "a"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for service with neither command nor docker")
	}
}

func TestValidateRejectsDockerWithoutImageOrBuild(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{{Name: "a", Docker: &DockerSpec{}}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for docker service with neither image nor build")
	}
}

func TestValidateAcceptsDockerBuild(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{
		{Name: "a", Docker: &DockerSpec{Build: &DockerBuild{Context: "svc"}}},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("docker build service should validate: %v", err)
	}
}

func TestValidateRejectsDockerBuildWithoutContext(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{
		{Name: "a", Docker: &DockerSpec{Build: &DockerBuild{}}},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for docker build without a context")
	}
}

func TestValidateRejectsSetupCheckWithoutSetup(t *testing.T) {
	// A setupCheck verifies that setup produced something, so it is meaningless
	// without setup commands (or a docker build to produce an image).
	c := &Config{Name: "x", Services: []Service{
		{Name: "a", Command: "run", SetupCheck: "test -f marker"},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for setupCheck without setup or docker.build")
	}
}

func TestValidateAcceptsSetupCheckWithSetup(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{
		{Name: "a", Command: "run", Setup: []string{"npm install"}, SetupCheck: "test -d node_modules"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("service with setup + setupCheck should validate: %v", err)
	}
}

func TestRestartPolicyDefaults(t *testing.T) {
	if got := (Service{}).Policy(); got != RestartNo {
		t.Fatalf("default policy = %q, want no", got)
	}
	if got := (Service{Restart: "always"}).Policy(); got != RestartAlways {
		t.Fatalf("policy = %q, want always", got)
	}
	if got := (Service{}).RestartLimit(); got != 5 {
		t.Fatalf("default limit = %d, want 5", got)
	}
	zero := 0
	if got := (Service{MaxRestarts: &zero}).RestartLimit(); got != 0 {
		t.Fatalf("limit = %d, want 0 (unlimited)", got)
	}
}

func TestValidateRejectsBadRestart(t *testing.T) {
	c := &Config{Name: "x", Services: []Service{{Name: "a", Command: "true", Restart: "sometimes"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid restart policy error")
	}
}

func profSvc(name string, profiles []string, deps ...string) Service {
	return Service{Name: name, Command: "true", Profiles: profiles, DependsOn: deps}
}

func selectedNames(t *testing.T, c *Config, only, profiles []string, deps bool) []string {
	t.Helper()
	svcs, err := c.SelectServices(only, profiles, deps)
	if err != nil {
		t.Fatalf("SelectServices: %v", err)
	}
	return names(svcs)
}

func TestSelectServicesProfiles(t *testing.T) {
	c := &Config{Services: []Service{
		profSvc("db", nil), // always on
		profSvc("api", []string{"backend"}, "db"),
		profSvc("web", []string{"frontend"}, "api"),
	}}

	// No profile: only the always-on service.
	if got := selectedNames(t, c, nil, nil, true); !equal(got, []string{"db"}) {
		t.Fatalf("no profile: got %v", got)
	}
	// frontend pulls web + its deps (api, db) in dependency order.
	if got := selectedNames(t, c, nil, []string{"frontend"}, true); !equal(got, []string{"db", "api", "web"}) {
		t.Fatalf("frontend+deps: got %v", got)
	}
	// frontend without deps: web plus the always-on db, but not api.
	if got := selectedNames(t, c, nil, []string{"frontend"}, false); !equal(got, []string{"db", "web"}) {
		t.Fatalf("frontend no-deps: got %v", got)
	}
}

func TestSelectServicesOnly(t *testing.T) {
	c := &Config{Services: []Service{
		profSvc("db", nil),
		profSvc("api", []string{"backend"}, "db"),
		profSvc("web", []string{"frontend"}, "api"),
	}}
	// --only api pulls db (dep) but not web.
	if got := selectedNames(t, c, []string{"api"}, nil, true); !equal(got, []string{"db", "api"}) {
		t.Fatalf("only api: got %v", got)
	}
	if _, err := c.SelectServices([]string{"ghost"}, nil, true); err == nil {
		t.Fatal("expected error for unknown --only service")
	}
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestRequiredToolsScopesToSelection(t *testing.T) {
	c := &Config{Requires: []Tool{
		{Name: "git"},                          // baseline: always required
		{Name: "docker", Services: []string{"postgres"}},
		{Name: "dotnet", Services: []string{"service"}},
		{Name: "node", Services: []string{"studio", "studio-cloud"}},
	}}

	// A cloud-only selection needs git (baseline) and node (studio-cloud lists
	// it), but neither docker nor dotnet — the point of the feature.
	got := toolNames(c.RequiredTools([]Service{{Name: "cloud-service"}, {Name: "studio-cloud"}}))
	if !equal(got, []string{"git", "node"}) {
		t.Fatalf("cloud selection: got %v", got)
	}

	// The local backend selection pulls docker + dotnet in, but not node.
	got = toolNames(c.RequiredTools([]Service{{Name: "postgres"}, {Name: "service"}}))
	if !equal(got, []string{"git", "docker", "dotnet"}) {
		t.Fatalf("backend selection: got %v", got)
	}

	// An empty selection yields only baseline tools — no partial run can avoid them.
	if got := toolNames(c.RequiredTools(nil)); !equal(got, []string{"git"}) {
		t.Fatalf("empty selection: got %v", got)
	}
}

func TestSelectRepos(t *testing.T) {
	c := &Config{Repos: []Repo{
		{Name: "shared", URL: "x"},
		{Name: "web", URL: "x", Profiles: []string{"frontend"}},
	}}
	if got := repoNames(c.SelectRepos(nil)); !equal(got, []string{"shared"}) {
		t.Fatalf("no profile repos: got %v", got)
	}
	if got := repoNames(c.SelectRepos([]string{"frontend"})); !equal(got, []string{"shared", "web"}) {
		t.Fatalf("frontend repos: got %v", got)
	}
}

func equal(a, b []string) bool {
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

func repoNames(r []Repo) []string {
	out := make([]string, len(r))
	for i, x := range r {
		out[i] = x.Name
	}
	return out
}

func TestValidateJob(t *testing.T) {
	ok := &Config{Name: "x", Services: []Service{{Name: "migrate", Kind: "job", Command: "run"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("job should be valid: %v", err)
	}
	// A job must not carry health/restart/watch (readiness is exit 0).
	bad := &Config{Name: "x", Services: []Service{{Name: "migrate", Kind: "job", Command: "run", Restart: "always"}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for a job with a restart policy")
	}
	if !(Service{Kind: "job"}).IsJob() || (Service{}).IsJob() {
		t.Fatal("IsJob() wrong")
	}
}

func TestValidateExternal(t *testing.T) {
	// External with no command/docker is valid.
	ok := &Config{Name: "x", Services: []Service{{Name: "cloud", External: true}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("external service should be valid: %v", err)
	}
	// External combined with a command is rejected.
	bad := &Config{Name: "x", Services: []Service{{Name: "cloud", External: true, Command: "run"}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for external service with a command")
	}
}

func TestRepoPath(t *testing.T) {
	if got := (Repo{Name: "api"}).Path(); got != "api" {
		t.Fatalf("default path = %q", got)
	}
	if got := (Repo{Name: "api", Dir: "svc/api"}).Path(); got != "svc/api" {
		t.Fatalf("override path = %q", got)
	}
}

func names(s []Service) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}
