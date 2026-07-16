// Package config defines the tarjan.yaml schema and its loading/validation.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/stevenzg/tarjan/internal/envfile"
)

// RepoConfigDir is the well-known directory inside a repository that carries
// the repo's own tarjan config (e.g. ".tarjan/tarjan.yaml"). A config loaded
// from it runs the repo in place, and repos cloned by a parent config
// contribute their .tarjan services to the parent's run.
const RepoConfigDir = ".tarjan"

// Config is the root of a tarjan.yaml file. It declares the repos, required
// tools, and services that make up a product's local development environment.
type Config struct {
	// Name identifies the product. Used for the workspace folder and the
	// generated VS Code workspace file.
	Name string `yaml:"name"`
	// Version labels the environment. When set, `tarjan up` materialises a stable,
	// reusable workspace named "<name>-<version>" instead of a timestamped one,
	// and reuses it on later runs (skipping clones/setup already done). Empty →
	// a fresh, timestamped workspace each run. Override per-run with `up --version`.
	Version string `yaml:"version"`
	// WorkspaceRoot is the directory under which each `tarjan up` materialises a
	// workspace. Supports ~ and $ENV expansion. Defaults to ~/tarjan/<name>.
	WorkspaceRoot string `yaml:"workspaceRoot"`

	// EnvFile lists .env files (relative to the config dir, or absolute) loaded
	// for every service. Useful for shared, git-ignored secrets.
	EnvFile []string `yaml:"envFile"`
	// Requires lists tools that must be present (and optionally a minimum
	// version) before the environment can start.
	Requires []Tool `yaml:"requires"`
	// Repos are git repositories cloned into the workspace.
	Repos []Repo `yaml:"repos"`
	// Services are processes (local or docker) that make up the running env.
	Services []Service `yaml:"services"`
	// Workspace controls IDE workspace generation.
	Workspace Workspace `yaml:"workspace"`
	// Hooks are global one-shot commands run around the environment lifecycle
	// (e.g. cloud auth, secret fetch).
	Hooks Hooks `yaml:"hooks"`
	// Remotes are named SSH execution targets. A service sets `remote: <name>`
	// to run there instead of on the local machine.
	Remotes map[string]Remote `yaml:"remotes"`

	// dir is the directory the config was loaded from (not serialised). For a
	// config inside a repo's .tarjan directory this is the repo root, so
	// relative paths resolve against the repo rather than the .tarjan folder.
	dir string `yaml:"-"`
	// inPlaceDir, when non-empty, is the repository root of a config loaded
	// from the repo's own .tarjan directory. Commands then treat that checkout
	// as the workspace instead of materialising one under WorkspaceRoot.
	inPlaceDir string `yaml:"-"`
}

// Remote is a named execution target reachable over SSH. A service that sets
// `remote: <name>` runs there instead of on the local machine: a process
// service's command runs over ssh; a docker service's container runs on the
// remote host's Docker daemon (via DOCKER_HOST=ssh://…). Published ports are
// tunnelled back to localhost, so local dependents and health checks keep
// using localhost unchanged.
type Remote struct {
	// Host is the SSH hostname (or an alias from the user's ssh config).
	Host string `yaml:"host"`
	// User is the SSH login user. Empty defers to the ssh config / current user.
	User string `yaml:"user"`
	// Port is the SSH port. 0 uses ssh's default (or the ssh config).
	Port int `yaml:"port"`
	// IdentityFile is an SSH private key passed as `ssh -i`. Optional.
	IdentityFile string `yaml:"identityFile"`
	// WorkspaceRoot is the base directory on the remote host under which a
	// process service's workdir resolves (and repos are cloned). Relative paths
	// are relative to the remote login directory. Defaults to "tarjan/<name>".
	WorkspaceRoot string `yaml:"workspaceRoot"`
	// Options are extra `ssh -o` options (e.g. "StrictHostKeyChecking=accept-new").
	Options []string `yaml:"options"`
	// Forward controls whether a remote service's published ports are tunnelled
	// back to the same localhost port. Defaults to true.
	Forward *bool `yaml:"forward"`
}

// Target returns the SSH destination "[user@]host".
func (r Remote) Target() string {
	if r.User != "" {
		return r.User + "@" + r.Host
	}
	return r.Host
}

// ForwardEnabled reports whether published ports are tunnelled back to
// localhost (the default).
func (r Remote) ForwardEnabled() bool {
	return r.Forward == nil || *r.Forward
}

// RemoteWorkspace returns the base directory on the remote host, defaulting to
// "tarjan/<name>" when unset.
func (r Remote) RemoteWorkspace(name string) string {
	if r.WorkspaceRoot != "" {
		return r.WorkspaceRoot
	}
	return "tarjan/" + name
}

// Hooks are global lifecycle commands, run in the workspace directory.
type Hooks struct {
	// PreUp runs once after repos are cloned and before any service starts —
	// the place for `gcloud auth login`, `aws sso login`, fetching secrets, etc.
	PreUp []string `yaml:"preUp"`
	// PostDown runs after the environment has stopped (cleanup).
	PostDown []string `yaml:"postDown"`
}

// Tool is an external dependency such as git, docker, node or dotnet.
type Tool struct {
	// Name is the executable looked up on PATH (e.g. "node").
	Name string `yaml:"name"`
	// MinVersion, if set, is compared against the detected version (semver-ish).
	MinVersion string `yaml:"minVersion"`
	// VersionCommand overrides how the version is probed. Default: "<name> --version".
	VersionCommand string `yaml:"versionCommand"`
	// InstallHint is shown to the user when the tool is missing.
	InstallHint string `yaml:"installHint"`
	// Install is a bespoke command that installs the tool, run only with opt-in
	// (`tarjan up --install` / `tarjan doctor --install`). It is either a single
	// command string or a map keyed by GOOS (darwin/linux/windows). It is the
	// escape hatch that overrides the general providers below.
	Install InstallSpec `yaml:"install"`
	// Mise installs the tool through the mise version manager
	// (https://mise.jdx.dev) — the general path for versioned language runtimes
	// (dotnet, node, python, go, java, flutter…). The value is a mise tool spec
	// such as "dotnet@10", or a bare tool name ("node") whose version is taken
	// from MinVersion. Runs only with --install; tarjan installs mise itself if
	// it is absent, and puts mise's shims on PATH so services use the managed
	// version.
	Mise string `yaml:"mise"`
	// Package installs the tool through the host's system package manager
	// (apt/brew/dnf/pacman/apk/zypper/choco/scoop/winget), auto-detected at run
	// time — the general path for OS client tools such as psql. It is either one
	// package name for every manager, or a map keyed by manager name when the
	// name differs (e.g. {apt: postgresql-client, brew: libpq}). Runs only with
	// --install.
	Package PackageSpec `yaml:"package"`
	// Optional tools only produce a warning when missing, not an error.
	Optional bool `yaml:"optional"`
}

// InstallSpec is a tool's install command: either one command for all
// platforms, or a per-OS map keyed by GOOS.
type InstallSpec struct {
	single string
	perOS  map[string]string
}

// UnmarshalYAML accepts either a scalar command or a mapping of GOOS -> command.
func (i *InstallSpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		i.single = node.Value
		return nil
	case yaml.MappingNode:
		return node.Decode(&i.perOS)
	default:
		return fmt.Errorf("install: expected a command string or a per-OS map")
	}
}

// NewInstall builds an InstallSpec programmatically (used by the Starlark
// loader). Provide either a single command or a per-OS map.
func NewInstall(single string, perOS map[string]string) InstallSpec {
	return InstallSpec{single: single, perOS: perOS}
}

// Command returns the install command for the given GOOS, or "" if none applies.
func (i InstallSpec) Command(goos string) string {
	if i.single != "" {
		return i.single
	}
	return i.perOS[goos]
}

// IsZero reports whether no install command was configured.
func (i InstallSpec) IsZero() bool {
	return i.single == "" && len(i.perOS) == 0
}

// PackageSpec is a tool's system package: either one package name for every
// package manager, or a map keyed by manager name (apt/brew/dnf/pacman/apk/
// zypper/choco/scoop/winget) for when the name differs across them.
type PackageSpec struct {
	single string
	perMgr map[string]string
}

// UnmarshalYAML accepts either a scalar package name or a mapping of
// manager -> package name.
func (p *PackageSpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		p.single = node.Value
		return nil
	case yaml.MappingNode:
		return node.Decode(&p.perMgr)
	default:
		return fmt.Errorf("package: expected a package name or a per-manager map")
	}
}

// NewPackage builds a PackageSpec programmatically (used by the Starlark
// loader). Provide either a single package name or a per-manager map.
func NewPackage(single string, perMgr map[string]string) PackageSpec {
	return PackageSpec{single: single, perMgr: perMgr}
}

// Name returns the package name for the given manager: the per-manager entry if
// present, otherwise the single default, or "" when neither applies.
func (p PackageSpec) Name(manager string) string {
	if n, ok := p.perMgr[manager]; ok {
		return n
	}
	return p.single
}

// IsZero reports whether no package was configured.
func (p PackageSpec) IsZero() bool {
	return p.single == "" && len(p.perMgr) == 0
}

// Repo is a git repository to clone into the workspace.
type Repo struct {
	// Name is the local folder name (and VS Code workspace label).
	Name string `yaml:"name"`
	// URL is the git clone URL.
	URL string `yaml:"url"`
	// Branch, if set, is checked out after clone.
	Branch string `yaml:"branch"`
	// Dir overrides the relative checkout path (default: Name).
	Dir string `yaml:"dir"`
	// Profiles gate the repo: with none it is always cloned; with one or more
	// it is cloned only when a matching profile is active.
	Profiles []string `yaml:"profiles"`
}

// IsJob reports whether the service runs to completion rather than staying up.
func (s Service) IsJob() bool { return s.Kind == "job" }

// ServiceEnv builds the environment for a service, layering (lowest to highest
// precedence): the current process env, the global env files, the service's env
// files, then inline env. Relative env-file paths resolve against the config
// directory. A missing or malformed env file is an error.
func (c *Config) ServiceEnv(spec Service) ([]string, error) {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	if err := c.mergeExtraEnv(spec, merged); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out, nil
}

// ServiceExtraEnv returns only the configured extra env for a service — the
// global env files, the service's env files, then inline env — without the
// inherited process environment. It backs remote execution, whose command runs
// on another host that must not inherit the local machine's environment. Keys
// are returned sorted, for a stable remote invocation.
func (c *Config) ServiceExtraEnv(spec Service) ([]string, error) {
	merged := map[string]string{}
	if err := c.mergeExtraEnv(spec, merged); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(merged))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, merged[k]))
	}
	return out, nil
}

// mergeExtraEnv layers the global env files, the service's env files, then
// inline env into merged (highest precedence last). Relative env-file paths
// resolve against the config directory; a missing or malformed file is an error.
func (c *Config) mergeExtraEnv(spec Service, merged map[string]string) error {
	apply := func(files []string) error {
		for _, f := range files {
			path := f
			if !filepath.IsAbs(path) {
				path = filepath.Join(c.dir, f)
			}
			pairs, err := envfile.Load(path)
			if err != nil {
				return fmt.Errorf("env file: %w", err)
			}
			for _, p := range pairs {
				merged[p.Key] = p.Value
			}
		}
		return nil
	}
	if err := apply(c.EnvFile); err != nil {
		return err
	}
	if err := apply(spec.EnvFile); err != nil {
		return err
	}
	for k, v := range spec.Env {
		merged[k] = v
	}
	return nil
}

// Path returns the repo's checkout path relative to the workspace root.
func (r Repo) Path() string {
	if r.Dir != "" {
		return r.Dir
	}
	return r.Name
}

// Service is a long-running process that is part of the dev environment.
type Service struct {
	// Name uniquely identifies the service.
	Name string `yaml:"name"`
	// Kind is "service" (default, long-running) or "job" (run-to-completion).
	// A job's dependents wait for it to exit 0, not for a health check — the
	// shape for migrations, seeds, and ETL/ML pipeline steps.
	Kind string `yaml:"kind"`
	// Workdir is the working directory (relative to the workspace) for setup
	// and command. Usually points at a cloned repo.
	Workdir string `yaml:"workdir"`
	// Setup are one-shot commands run before the service starts the first time
	// (e.g. "npm install", "dotnet restore").
	Setup []string `yaml:"setup"`
	// SetupCheck, if set, is a shell command that verifies Setup actually
	// provisioned the workdir — setup is recorded as complete only when this
	// exits 0. It closes the gap where a setup command reports success without
	// producing what it should (e.g. `npm install` printing "up to date" while a
	// package's postinstall-downloaded binary is missing): without a check that
	// broken state is frozen as "done" and a plain re-run can never repair it.
	// On a later run the check re-verifies the cached workspace and re-runs Setup
	// if it now fails. Runs in the Workdir with the service's env, like Setup.
	// Empty → setup completion is tracked only by the marker (a command exiting 0).
	SetupCheck string `yaml:"setupCheck"`
	// Command is the long-running command (e.g. "npm run dev"). Ignored when
	// Docker is set.
	Command string `yaml:"command"`
	// Env are extra environment variables for setup and command.
	Env map[string]string `yaml:"env"`
	// EnvFile lists .env files (relative to the config dir, or absolute) loaded
	// for this service, layered after the global EnvFile and before inline Env.
	EnvFile []string `yaml:"envFile"`
	// DependsOn lists services that must be healthy before this one starts.
	DependsOn []string `yaml:"dependsOn"`
	// Docker, if set, runs the service as a container instead of a local process.
	Docker *DockerSpec `yaml:"docker"`
	// External marks a service that tarjan does not start — a cloud/remote
	// dependency. It is only health-probed for reachability, and local services
	// may dependsOn it. It has no command/docker/restart/watch.
	External bool `yaml:"external"`
	// Health describes how to know the service is ready for its dependents.
	Health *Health `yaml:"health"`
	// Optional services log a warning instead of failing the whole `up`.
	Optional bool `yaml:"optional"`
	// Restart is the crash-recovery policy: "no" (default), "on-failure"
	// (restart only on non-zero exit), or "always".
	Restart string `yaml:"restart"`
	// MaxRestarts caps automatic restarts. Defaults to 5; 0 means unlimited.
	// Restarts triggered by Watch do not count against this.
	MaxRestarts *int `yaml:"maxRestarts"`
	// Watch restarts the service when files under the given paths change.
	Watch *Watch `yaml:"watch"`
	// Profiles gate the service: with none it always runs; with one or more it
	// runs only when a matching profile is active (or it is named in --only, or
	// pulled in as a dependency).
	Profiles []string `yaml:"profiles"`
	// Remote names an entry in the top-level `remotes` map. When set, the
	// service runs on that host (a process over ssh, a docker container on the
	// remote daemon) instead of locally. Empty means run locally.
	Remote string `yaml:"remote"`
}

// IsRemote reports whether the service runs on a named remote target.
func (s Service) IsRemote() bool { return s.Remote != "" }

// Watch declares files whose changes trigger a live restart of the service.
type Watch struct {
	// Paths are files or directories (relative to the service Workdir) watched
	// recursively for modifications.
	Paths []string `yaml:"paths"`
	// Debounce is how long to wait for changes to settle before restarting
	// (default "300ms").
	Debounce string `yaml:"debounce"`
}

// RestartPolicy is the validated restart mode for a service.
type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// Policy returns the service's restart policy, defaulting to "no".
func (s Service) Policy() RestartPolicy {
	switch s.Restart {
	case string(RestartOnFailure):
		return RestartOnFailure
	case string(RestartAlways):
		return RestartAlways
	default:
		return RestartNo
	}
}

// RestartLimit returns the max automatic restarts (default 5; 0 = unlimited).
func (s Service) RestartLimit() int {
	if s.MaxRestarts == nil {
		return 5
	}
	return *s.MaxRestarts
}

// DockerSpec runs a service as a docker container.
type DockerSpec struct {
	// Image is the published image to pull and run. Mutually exclusive with
	// Build (when both are set, Image names the tag the build produces).
	Image string `yaml:"image"`
	// Build, if set, builds a local image from source (a cloned repo) instead
	// of pulling a published Image — the equivalent of `docker compose build`
	// for services that ship only a Dockerfile.
	Build   *DockerBuild      `yaml:"build"`
	Ports   []string          `yaml:"ports"` // "host:container"
	Env     map[string]string `yaml:"env"`
	Volumes []string          `yaml:"volumes"` // "host:container"
	// Args are extra `docker run` options inserted before the image.
	Args []string `yaml:"args"`
	// Command overrides the image's default CMD — the arguments placed after
	// the image in `docker run`. Use it to select one of several entrypoints an
	// image supports (e.g. run the server without its bundled migration step).
	Command []string `yaml:"command"`
}

// DockerBuild builds a service's image from a local source context instead of
// pulling a published image. tarjan builds it once per workspace (like a setup
// step) and then runs the resulting tag like any other docker service.
type DockerBuild struct {
	// Context is the build context directory, relative to the workspace
	// (usually a cloned repo's path).
	Context string `yaml:"context"`
	// Dockerfile overrides the Dockerfile path, relative to Context.
	Dockerfile string `yaml:"dockerfile"`
	// Args are --build-arg values passed to the build.
	Args map[string]string `yaml:"args"`
}

// Health describes a readiness probe. The first non-empty field is used.
type Health struct {
	// TCP dials "host:port" until it connects.
	TCP string `yaml:"tcp"`
	// HTTP issues GET requests until a 2xx/3xx response.
	HTTP string `yaml:"http"`
	// Command runs until it exits 0.
	Command string `yaml:"command"`
	// Timeout is the overall deadline (default "60s").
	Timeout string `yaml:"timeout"`
	// Interval between probe attempts (default "1s").
	Interval string `yaml:"interval"`
}

// Workspace controls IDE workspace generation.
type Workspace struct {
	// VSCode generates a <name>.code-workspace including every cloned repo.
	VSCode bool `yaml:"vscode"`
}

// Dir returns the directory the config was loaded from.
func (c *Config) Dir() string { return c.dir }

// SetDir sets the base directory used to resolve relative paths (e.g. env
// files). Load sets this automatically; it is exposed for configs built
// programmatically.
func (c *Config) SetDir(dir string) { c.dir = dir }

// InPlaceDir returns the repository root when the config was loaded from the
// repo's own .tarjan directory, or "" for a regular top-level config.
func (c *Config) InPlaceDir() string { return c.inPlaceDir }

// SetInPlaceDir marks the config as loaded from a repo's .tarjan directory,
// with dir as the repository root. Loaders set this automatically.
func (c *Config) SetInPlaceDir(dir string) { c.inPlaceDir = dir }

// ResolveDir returns the base directory for a config file at path. When the
// file lives in a repo's .tarjan directory, dir is the repository root (the
// .tarjan parent) and inPlace is set to it; otherwise inPlace is "".
func ResolveDir(path string) (dir, inPlace string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	dir = filepath.Dir(abs)
	if filepath.Base(dir) == RepoConfigDir {
		inPlace = filepath.Dir(dir)
		dir = inPlace
	}
	return dir, inPlace, nil
}

// Load reads and validates a tarjan.yaml from the given file path.
func Load(path string) (*Config, error) {
	c, dir, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if err := c.Finalize(dir); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadFragment reads a per-repo config fragment from path, applying defaults
// and path expansion but skipping structural validation: a fragment may
// reference services (dependsOn) that only exist once it is merged into its
// parent config, which is validated as a whole afterwards.
func LoadFragment(path string) (*Config, error) {
	c, dir, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if err := c.FinalizeFragment(dir); err != nil {
		return nil, err
	}
	return c, nil
}

// parseFile reads and unmarshals a YAML config, returning it with its
// resolved base directory (and inPlaceDir set when applicable).
func parseFile(path string) (*Config, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", path, err)
	}
	dir, inPlace, err := ResolveDir(path)
	if err != nil {
		return nil, "", err
	}
	c.inPlaceDir = inPlace
	return &c, dir, nil
}

// Finalize applies defaults, resolves paths relative to dir, and validates a
// config. Load calls it automatically; programmatic builders (e.g. the Starlark
// loader) call it after constructing a Config.
func (c *Config) Finalize(dir string) error {
	c.dir = dir
	if err := c.normalize(); err != nil {
		return err
	}
	return c.Validate()
}

// FinalizeFragment applies defaults and path expansion without structural
// validation — for per-repo config fragments, which are validated only after
// being merged into the parent config.
func (c *Config) FinalizeFragment(dir string) error {
	c.dir = dir
	return c.normalize()
}

// normalize applies defaults and expands paths.
func (c *Config) normalize() error {
	if c.Name == "" {
		c.Name = filepath.Base(c.dir)
	}
	if c.WorkspaceRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		c.WorkspaceRoot = filepath.Join(home, "tarjan", c.Name)
	}
	c.WorkspaceRoot = expandPath(c.WorkspaceRoot)
	return nil
}

// expandPath expands a leading ~ and any $ENV references.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			// filepath.Join rather than string concatenation so the result uses the
			// OS separator throughout — "~/x" must not yield "C:\Users\me/x" on
			// Windows (mixed separators).
			p = filepath.Join(home, p[1:])
		}
	}
	return os.ExpandEnv(p)
}

// Validate checks for structural errors: duplicate names, dangling
// dependencies and dependency cycles.
func (c *Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("config: name is required")
	}
	for name, r := range c.Remotes {
		if name == "" {
			return fmt.Errorf("remote: name is required")
		}
		if r.Host == "" {
			return fmt.Errorf("remote %q: host is required", name)
		}
		// A host/user beginning with '-' would be parsed by ssh as an option
		// rather than the destination (e.g. "-oProxyCommand=…" → local command
		// execution). ssh does not reliably honour a "--" terminator before the
		// destination, so reject it at the source. This matters because a cloned
		// repo's own .tarjan config can contribute remotes (see repocfg.merge).
		if strings.HasPrefix(r.Host, "-") {
			return fmt.Errorf("remote %q: host %q must not begin with '-'", name, r.Host)
		}
		if strings.HasPrefix(r.User, "-") {
			return fmt.Errorf("remote %q: user %q must not begin with '-'", name, r.User)
		}
	}
	seenRepo := map[string]bool{}
	seenDir := map[string]bool{}
	for _, r := range c.Repos {
		if r.Name == "" || r.URL == "" {
			return fmt.Errorf("repo: name and url are required")
		}
		if seenRepo[r.Name] {
			return fmt.Errorf("repo: duplicate name %q", r.Name)
		}
		seenRepo[r.Name] = true
		if !safeRelPath(r.Path()) {
			return fmt.Errorf("repo %q: checkout path %q must stay inside the workspace (no absolute paths or \"..\")", r.Name, r.Path())
		}
		if seenDir[r.Path()] {
			return fmt.Errorf("repo %q: duplicate checkout path %q", r.Name, r.Path())
		}
		seenDir[r.Path()] = true
	}

	svcByName := map[string]*Service{}
	for i := range c.Services {
		s := &c.Services[i]
		if s.Name == "" {
			return fmt.Errorf("service: name is required")
		}
		if _, ok := svcByName[s.Name]; ok {
			return fmt.Errorf("service: duplicate name %q", s.Name)
		}
		switch s.Kind {
		case "", "service", "job":
		default:
			return fmt.Errorf("service %q: invalid kind %q (want service|job)", s.Name, s.Kind)
		}
		if !safeRelPath(s.Workdir) {
			return fmt.Errorf("service %q: workdir %q must stay inside the workspace (no absolute paths or \"..\")", s.Name, s.Workdir)
		}
		if s.MaxRestarts != nil && *s.MaxRestarts < 0 {
			return fmt.Errorf("service %q: maxRestarts must be >= 0", s.Name)
		}
		if s.SetupCheck != "" && len(s.Setup) == 0 && (s.Docker == nil || s.Docker.Build == nil) {
			return fmt.Errorf("service %q: setupCheck needs setup commands or docker.build to verify", s.Name)
		}
		if s.Remote != "" {
			if _, ok := c.Remotes[s.Remote]; !ok {
				return fmt.Errorf("service %q: remote %q is not defined in remotes", s.Name, s.Remote)
			}
			if s.External {
				return fmt.Errorf("service %q: external services cannot set remote", s.Name)
			}
			if s.Watch != nil {
				return fmt.Errorf("service %q: watch is not supported for remote services", s.Name)
			}
		}
		if s.External {
			if s.IsJob() || s.Command != "" || s.Docker != nil || s.Restart != "" || s.Watch != nil {
				return fmt.Errorf("service %q: external services take no kind/command/docker/restart/watch", s.Name)
			}
			svcByName[s.Name] = s
			continue
		}
		if s.Docker == nil && s.Command == "" {
			return fmt.Errorf("service %q: either command, docker, or external is required", s.Name)
		}
		if s.Docker != nil {
			if s.Docker.Image == "" && s.Docker.Build == nil {
				return fmt.Errorf("service %q: docker requires image or build", s.Name)
			}
			if s.Docker.Build != nil && s.Docker.Build.Context == "" {
				return fmt.Errorf("service %q: docker.build requires a context", s.Name)
			}
		}
		if s.IsJob() && (s.Restart != "" || s.Watch != nil || s.Health != nil) {
			return fmt.Errorf("service %q: jobs take no restart/watch/health (readiness is exit 0)", s.Name)
		}
		switch s.Restart {
		case "", string(RestartNo), string(RestartOnFailure), string(RestartAlways):
		default:
			return fmt.Errorf("service %q: invalid restart %q (want no|on-failure|always)", s.Name, s.Restart)
		}
		svcByName[s.Name] = s
	}
	for _, s := range c.Services {
		for _, dep := range s.DependsOn {
			if _, ok := svcByName[dep]; !ok {
				return fmt.Errorf("service %q: dependsOn unknown service %q", s.Name, dep)
			}
		}
	}
	if _, err := c.StartOrder(); err != nil {
		return err
	}
	return nil
}

// safeRelPath reports whether p is safe to join under the workspace root: empty
// (meaning the workspace itself), or a relative path that cannot escape it. It
// rejects absolute paths and any path that, once cleaned, starts with ".." — the
// guard against a config pointing a checkout or workdir outside the workspace.
func safeRelPath(p string) bool {
	if p == "" {
		return true
	}
	// filepath.IsAbs is OS-specific: on Windows it does not treat a leading-slash
	// path like "/etc/passwd" as absolute (it wants a drive or UNC), so reject a
	// leading slash/backslash explicitly to catch such escapes on every OS.
	if filepath.IsAbs(p) || p[0] == '/' || p[0] == '\\' {
		return false
	}
	// Normalise separators so a Windows-style "..\x" is caught on any OS.
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// hasActiveProfile reports whether the given profiles make an item active: an
// item with no profiles is always active; otherwise any overlap with active
// suffices.
func hasActiveProfile(profiles []string, active map[string]bool) bool {
	if len(profiles) == 0 {
		return true
	}
	for _, p := range profiles {
		if active[p] {
			return true
		}
	}
	return false
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, i := range items {
		if i != "" {
			set[i] = true
		}
	}
	return set
}

// SelectServices resolves which services to run, in dependency order.
//
//   - only: if non-empty, exactly these services are seeded (profiles ignored
//     for the named ones), otherwise every profile-active service is seeded.
//   - profiles: the active profile names.
//   - includeDeps: when true, dependencies of seeded services are pulled in
//     transitively; when false, only the seed is returned.
func (c *Config) SelectServices(only, profiles []string, includeDeps bool) ([]Service, error) {
	byName := map[string]Service{}
	for _, s := range c.Services {
		byName[s.Name] = s
	}
	active := toSet(profiles)
	selected := map[string]bool{}

	if len(only) > 0 {
		for _, n := range only {
			if _, ok := byName[n]; !ok {
				return nil, fmt.Errorf("--only: unknown service %q", n)
			}
			selected[n] = true
		}
	} else {
		for _, s := range c.Services {
			if hasActiveProfile(s.Profiles, active) {
				selected[s.Name] = true
			}
		}
	}

	if includeDeps {
		var add func(name string)
		add = func(name string) {
			for _, dep := range byName[name].DependsOn {
				if !selected[dep] {
					selected[dep] = true
					add(dep)
				}
			}
		}
		for name := range selected {
			add(name)
		}
	}

	order, err := c.startOrder(byName)
	if err != nil {
		return nil, err
	}
	out := order[:0:0]
	for _, s := range order {
		if selected[s.Name] {
			out = append(out, s)
		}
	}
	return out, nil
}

// SelectRepos returns the repos to clone for the active profiles.
func (c *Config) SelectRepos(profiles []string) []Repo {
	active := toSet(profiles)
	var out []Repo
	for _, r := range c.Repos {
		if hasActiveProfile(r.Profiles, active) {
			out = append(out, r)
		}
	}
	return out
}

// serviceIndex builds a name→service lookup over the config's services.
func (c *Config) serviceIndex() map[string]Service {
	byName := make(map[string]Service, len(c.Services))
	for _, s := range c.Services {
		byName[s.Name] = s
	}
	return byName
}

// StartOrder returns services in dependency order (dependencies first) using a
// topological sort. It returns an error if a cycle is detected.
func (c *Config) StartOrder() ([]Service, error) {
	return c.startOrder(c.serviceIndex())
}

// startOrder is StartOrder over a caller-supplied index, so a caller that has
// already built the name→service map (e.g. SelectServices) need not rebuild it.
func (c *Config) startOrder(byName map[string]Service) ([]Service, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // done
	)
	color := map[string]int{}
	var order []Service
	var visit func(name string) error
	visit = func(name string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("service dependency cycle detected at %q", name)
		case black:
			return nil
		}
		color[name] = gray
		for _, dep := range byName[name].DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		color[name] = black
		order = append(order, byName[name])
		return nil
	}
	for _, s := range c.Services {
		if err := visit(s.Name); err != nil {
			return nil, err
		}
	}
	return order, nil
}
