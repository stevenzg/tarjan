package deps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/shellx"
	"github.com/stevenzg/tarjan/internal/ui"
)

// installPlan is a resolved, runnable way to install one tool on this machine,
// together with a human description of exactly what it will run — shown both
// before running (in the --install-less error) and while running.
type installPlan struct {
	desc string
	run  func() error
}

// resolveInstall returns how to install t on this OS, or nil when nothing is
// configured. Precedence, most explicit first: a bespoke install: command, then
// a mise: version-manager spec, then a system package:. This lets a config
// override the general providers for one awkward tool while leaving the rest to
// mise/the package manager.
func resolveInstall(t config.Tool) *installPlan {
	if cmd := t.Install.Command(runtime.GOOS); cmd != "" {
		return &installPlan{desc: cmd, run: func() error { return runShell(t.Name, cmd) }}
	}
	if spec := miseSpec(t); spec != "" {
		return &installPlan{
			desc: "mise use --global " + spec,
			run:  func() error { return miseInstall(t.Name, spec) },
		}
	}
	if !t.Package.IsZero() {
		if mgr, pkg := resolvePackage(t.Package); mgr != nil {
			bin, args := mgr.argv(pkg)
			desc := mgr.installCmd(pkg)
			return &installPlan{desc: desc, run: func() error { return runArgv(t.Name, desc, bin, args) }}
		}
	}
	return nil
}

// runShell runs an install command line through the OS shell, streaming output
// so the user sees the package manager's own progress. It is used only for the
// bespoke `install:` escape hatch, which is a shell command by definition.
func runShell(name, cmdline string) error {
	ui.Info("installing %s: %s", name, cmdline)
	sh, args := shellx.Command(cmdline)
	cmd := exec.Command(sh, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// runArgv runs an install command as an explicit argv (no shell), so a package
// name from config can never be interpreted as shell syntax — a package like
// "foo; rm -rf /" is passed as a single argument to the package manager, not
// executed. desc is the human-readable form shown to the user.
func runArgv(name, desc, bin string, args []string) error {
	ui.Info("installing %s: %s", name, desc)
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// --- mise version manager -------------------------------------------------

// miseSpec returns the mise tool spec for t: the configured mise: value, with a
// version from MinVersion appended when the spec names a bare tool. Returns ""
// when the tool does not use mise.
func miseSpec(t config.Tool) string {
	if t.Mise == "" {
		return ""
	}
	spec := t.Mise
	if !strings.Contains(spec, "@") && t.MinVersion != "" {
		spec += "@" + t.MinVersion
	}
	return spec
}

// miseInstaller is mise's official one-line bootstrap. It drops the binary in
// ~/.local/bin, which activateMise then puts on PATH for the rest of the run.
const miseInstaller = "curl -fsSL https://mise.run | sh"

// miseInstall installs and globally selects a mise-managed tool, bootstrapping
// mise itself when necessary, then makes mise's shims visible on PATH so the
// tool — and the services that later run it — resolve to the managed version.
func miseInstall(name, spec string) error {
	mise, err := ensureMise()
	if err != nil {
		return err
	}
	ui.Info("installing %s: %s use --global %s", name, mise, spec)
	cmd := exec.Command(mise, "use", "--global", spec)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	activateMise()
	return nil
}

// ensureMise returns a path to the mise binary, installing mise via its official
// installer when it is not already present.
func ensureMise() (string, error) {
	if mise := findMise(); mise != "" {
		return mise, nil
	}
	ui.Info("installing mise (version manager): %s", miseInstaller)
	sh, args := shellx.Command(miseInstaller)
	cmd := exec.Command(sh, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("bootstrap mise: %w (install it from https://mise.jdx.dev)", err)
	}
	if mise := findMise(); mise != "" {
		return mise, nil
	}
	return "", fmt.Errorf("mise installed but not found; add ~/.local/bin to PATH and re-run")
}

// findMise returns a usable mise binary path — from PATH, or mise's default
// ~/.local/bin location when that directory is not itself on PATH — or "" when
// mise is not installed. The ~/.local/bin fallback matters because mise's
// installer puts the binary there without necessarily adding it to the user's
// shell PATH, so a fresh `tarjan up` would otherwise not see it.
func findMise() string {
	if p, err := exec.LookPath("mise"); err == nil {
		return p
	}
	if local := userLocalMise(); local != "" && isExec(local) {
		return local
	}
	return ""
}

// activateMise makes mise and its shims usable for this process and the services
// it spawns. When mise resolves only via ~/.local/bin (a bootstrap install whose
// directory the user hasn't put on PATH), that directory is added too, so the
// shims — which exec `mise` — work. Reports whether mise was found.
func activateMise() bool {
	mise := findMise()
	if mise == "" {
		return false
	}
	if _, err := exec.LookPath("mise"); err != nil {
		prependPath(filepath.Dir(mise))
	}
	prependMiseShims()
	return true
}

// userLocalMise is mise's default install location, used when ~/.local/bin is
// not yet on PATH.
func userLocalMise() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := "mise"
	if runtime.GOOS == "windows" {
		name = "mise.exe"
	}
	return filepath.Join(home, ".local", "bin", name)
}

// miseShimsDir is where mise writes the shims that expose managed tools without
// shell activation — the mechanism that lets tarjan-spawned services find them.
func miseShimsDir() string {
	if d := os.Getenv("MISE_DATA_DIR"); d != "" {
		return filepath.Join(d, "shims")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "mise", "shims")
}

// prependMiseShims puts mise's shims directory on PATH (once) so both this
// process and the services it starts resolve mise-managed tools.
func prependMiseShims() {
	if dir := miseShimsDir(); dir != "" {
		if _, err := os.Stat(dir); err == nil {
			prependPath(dir)
		}
	}
}

// pinFiles are the project-level files by which a repo pins its own tool
// versions for mise (mise-native configs and the asdf-compatible
// .tool-versions). A repo carrying one owns its runtime versions; the
// environment's requires: entries are only the baseline for repos that don't.
var pinFiles = []string{
	"mise.toml",
	"mise.local.toml",
	".mise.toml",
	".mise.local.toml",
	".tool-versions",
	filepath.Join(".mise", "config.toml"),
	filepath.Join(".config", "mise", "config.toml"),
}

// hasWorkdirPins reports whether dir pins its own tool versions.
func hasWorkdirPins(dir string) bool {
	for _, f := range pinFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// InstallWorkdirPins materialises the tool versions a service's workdir pins for
// itself (mise.toml / .tool-versions): it runs `mise install` in that directory,
// so each repo gets exactly the runtime it declares — different repos can pin
// different versions of the same tool, and a repo's pin overrides the
// environment baseline from requires:. It is a no-op when the workdir pins
// nothing, and idempotent (mise skips versions already installed) so it is safe
// to run on every up — a pull can change the pins even in a reused workspace.
// When pins exist but mise is not installed it warns and continues, since the
// system-wide tools may still satisfy the service.
func InstallWorkdirPins(ctx context.Context, name, dir string) error {
	if !hasWorkdirPins(dir) {
		return nil
	}
	if !activateMise() {
		ui.Warn("%s: workdir pins tool versions (mise config) but mise is not installed — using system tools", name)
		return nil
	}
	mise := findMise()
	ui.Step("%s: mise install (workdir pins its tool versions)", name)
	cmd := exec.CommandContext(ctx, mise, "install")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mise install: %w", err)
	}
	// A first-ever mise install may have just created the shims directory.
	prependMiseShims()
	return nil
}

// ensureMisePATH activates an already-installed mise before probing, so a tool
// already managed by mise resolves on PATH even without --install (and the
// services that need it inherit the same PATH) — including when mise lives in
// ~/.local/bin off the user's PATH.
func ensureMisePATH(tools []config.Tool) {
	for _, t := range tools {
		if t.Mise != "" {
			activateMise()
			return
		}
	}
}

// pathMu serialises PATH mutations. Up starts every service concurrently and
// each may activate mise / prepend a shims dir, so the read-modify-write below
// must be atomic or one goroutine's update clobbers another's.
var pathMu sync.Mutex

// prependPath adds dir to the front of PATH for this process (and thus every
// child it spawns), unless it is already present.
func prependPath(dir string) {
	pathMu.Lock()
	defer pathMu.Unlock()
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return
		}
	}
	if path == "" {
		_ = os.Setenv("PATH", dir)
		return
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+path)
}

func isExec(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

// --- system package managers ----------------------------------------------

// pkgManager describes how to install a named package with one system package
// manager. The install command is held as a structured argv (subcommand args,
// with the package name appended last) rather than a format string, so the
// package name is never interpreted by a shell.
type pkgManager struct {
	name string   // key used in a per-manager package: map (apt/brew/dnf/…)
	bin  string   // executable probed on PATH to detect the manager
	sudo bool     // whether the install needs root via sudo
	args []string // install subcommand args; the package name is appended
}

// argv returns the executable and argument vector to install pkg — with the
// package name as its own final argument, never spliced into a command string.
func (m pkgManager) argv(pkg string) (string, []string) {
	full := append(append([]string{}, m.args...), pkg)
	if m.sudo {
		return "sudo", append([]string{m.bin}, full...)
	}
	return m.bin, full
}

// installCmd is the human-readable command line for pkg — used to describe what
// --install would run (and in tests). Execution goes through argv, not this.
func (m pkgManager) installCmd(pkg string) string {
	bin, args := m.argv(pkg)
	return bin + " " + strings.Join(args, " ")
}

// pkgManagers lists the supported managers for a GOOS, in detection order — the
// first one found on PATH wins.
func pkgManagers(goos string) []pkgManager {
	switch goos {
	case "darwin":
		return []pkgManager{{name: "brew", bin: "brew", args: []string{"install"}}}
	case "windows":
		return []pkgManager{
			{name: "winget", bin: "winget", args: []string{"install", "-e", "--id"}},
			{name: "choco", bin: "choco", args: []string{"install", "-y"}},
			{name: "scoop", bin: "scoop", args: []string{"install"}},
		}
	default: // linux and other unixes
		return []pkgManager{
			{name: "apt", bin: "apt-get", sudo: true, args: []string{"install", "-y"}},
			{name: "dnf", bin: "dnf", sudo: true, args: []string{"install", "-y"}},
			{name: "yum", bin: "yum", sudo: true, args: []string{"install", "-y"}},
			{name: "pacman", bin: "pacman", sudo: true, args: []string{"-S", "--noconfirm"}},
			{name: "zypper", bin: "zypper", sudo: true, args: []string{"install", "-y"}},
			{name: "apk", bin: "apk", sudo: true, args: []string{"add"}},
		}
	}
}

// resolvePackage picks the first available package manager and the package name
// to use with it. It returns nil when no supported manager is on PATH, or the
// spec names no package for the one that is.
func resolvePackage(spec config.PackageSpec) (*pkgManager, string) {
	for _, m := range pkgManagers(runtime.GOOS) {
		if _, err := exec.LookPath(m.bin); err != nil {
			continue
		}
		if pkg := spec.Name(m.name); pkg != "" {
			mgr := m
			return &mgr, pkg
		}
	}
	return nil, ""
}
