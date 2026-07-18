package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestMiseSpec(t *testing.T) {
	cases := []struct {
		name string
		tool config.Tool
		want string
	}{
		{"explicit version", config.Tool{Mise: "dotnet@10"}, "dotnet@10"},
		{"bare tool uses minVersion", config.Tool{Mise: "node", MinVersion: "20"}, "node@20"},
		{"bare tool no minVersion", config.Tool{Mise: "flutter"}, "flutter"},
		{"explicit version wins over minVersion", config.Tool{Mise: "go@1.22", MinVersion: "1.20"}, "go@1.22"},
		{"no mise", config.Tool{Name: "git"}, ""},
	}
	for _, c := range cases {
		if got := miseSpec(c.tool); got != c.want {
			t.Errorf("%s: miseSpec = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPkgManagerInstallCmd(t *testing.T) {
	cases := map[string]struct {
		goos, mgr, pkg, want string
	}{
		"apt":    {"linux", "apt", "postgresql-client", "sudo apt-get install -y postgresql-client"},
		"dnf":    {"linux", "dnf", "postgresql", "sudo dnf install -y postgresql"},
		"pacman": {"linux", "pacman", "postgresql", "sudo pacman -S --noconfirm postgresql"},
		"brew":   {"darwin", "brew", "libpq", "brew install libpq"},
		"scoop":  {"windows", "scoop", "postgresql", "scoop install postgresql"},
	}
	for name, c := range cases {
		var found bool
		for _, m := range pkgManagers(c.goos) {
			if m.name == c.mgr {
				found = true
				if got := m.installCmd(c.pkg); got != c.want {
					t.Errorf("%s: installCmd = %q, want %q", name, got, c.want)
				}
			}
		}
		if !found {
			t.Errorf("%s: manager %q not listed for %s", name, c.mgr, c.goos)
		}
	}
}

// TestPkgManagerArgvNoShell checks the install is built as an explicit argv with
// the package name as its own final argument — so a package name carrying shell
// metacharacters cannot be interpreted as a command (the S2 injection guard).
func TestPkgManagerArgvNoShell(t *testing.T) {
	var apt pkgManager
	for _, m := range pkgManagers("linux") {
		if m.name == "apt" {
			apt = m
		}
	}
	if apt.name == "" {
		t.Fatal("apt manager not listed for linux")
	}
	bin, args := apt.argv("foo; rm -rf /")
	if bin != "sudo" {
		t.Fatalf("bin = %q, want sudo", bin)
	}
	// The malicious package name must survive intact as the single trailing arg,
	// never split into further tokens (which a shell would do).
	if got := args[len(args)-1]; got != "foo; rm -rf /" {
		t.Fatalf("package arg = %q, want it passed verbatim as one argument", got)
	}
	want := []string{"apt-get", "install", "-y", "foo; rm -rf /"}
	if len(args) != len(want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("argv = %v, want %v", args, want)
		}
	}
}

// TestResolveInstallPrecedence checks install: overrides mise:, which overrides
// package:, without touching the host package managers.
func TestResolveInstallPrecedence(t *testing.T) {
	// install: wins.
	tool := config.Tool{Name: "x", Install: config.NewInstall("do-install", nil), Mise: "x@1"}
	if p := resolveInstall(tool); p == nil || p.desc != "do-install" {
		t.Fatalf("install: should win, got %+v", p)
	}
	// mise: wins over package:.
	tool = config.Tool{Name: "x", Mise: "x@1", Package: config.NewPackage("x-pkg", nil)}
	if p := resolveInstall(tool); p == nil || p.desc != "mise use --global x@1" {
		t.Fatalf("mise: should win over package, got %+v", p)
	}
	// nothing configured → no plan.
	if p := resolveInstall(config.Tool{Name: "git"}); p != nil {
		t.Fatalf("no provider should yield no plan, got %+v", p)
	}
}

// TestResolvePackageDetectsManager fabricates a package manager on PATH and
// checks resolvePackage selects it and its package name. Linux-only because the
// candidate manager list is keyed on the host OS.
func TestResolvePackageDetectsManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("apt-get is only a candidate on linux")
	}
	bin := t.TempDir()
	writeFakeExec(t, filepath.Join(bin, "apt-get"))
	t.Setenv("PATH", bin) // only our fake manager is discoverable

	spec := config.NewPackage("", map[string]string{"apt": "postgresql-client"})
	mgr, pkg := resolvePackage(spec)
	if mgr == nil || mgr.name != "apt" {
		t.Fatalf("expected apt manager, got %+v", mgr)
	}
	if pkg != "postgresql-client" {
		t.Fatalf("package = %q", pkg)
	}
	if got := mgr.installCmd(pkg); got != "sudo apt-get install -y postgresql-client" {
		t.Fatalf("installCmd = %q", got)
	}
}

// TestPkgManagerQueryArgv checks each manager's presence query is the right tool
// and argv (the package name as its own final argument, never shell-spliced),
// and that the Windows managers declare no query.
func TestPkgManagerQueryArgv(t *testing.T) {
	cases := map[string]struct {
		goos, mgr, pkg, wantBin string
		wantArgs                []string
	}{
		"apt":    {"linux", "apt", "libnspr4", "dpkg", []string{"-s", "libnspr4"}},
		"dnf":    {"linux", "dnf", "nspr", "rpm", []string{"-q", "nspr"}},
		"pacman": {"linux", "pacman", "nss", "pacman", []string{"-Q", "nss"}},
		"apk":    {"linux", "apk", "nss", "apk", []string{"info", "-e", "nss"}},
		"brew":   {"darwin", "brew", "nss", "brew", []string{"list", "--versions", "nss"}},
	}
	for name, c := range cases {
		var m pkgManager
		for _, cand := range pkgManagers(c.goos) {
			if cand.name == c.mgr {
				m = cand
			}
		}
		if m.name == "" {
			t.Errorf("%s: manager %q not listed for %s", name, c.mgr, c.goos)
			continue
		}
		bin, args := m.queryArgv(c.pkg)
		if bin != c.wantBin {
			t.Errorf("%s: queryArgv bin = %q, want %q", name, bin, c.wantBin)
		}
		if strings.Join(args, " ") != strings.Join(c.wantArgs, " ") {
			t.Errorf("%s: queryArgv args = %v, want %v", name, args, c.wantArgs)
		}
	}
	for _, m := range pkgManagers("windows") {
		if bin, _ := m.queryArgv("x"); bin != "" {
			t.Errorf("windows manager %q should declare no query, got %q", m.name, bin)
		}
	}
}

// TestPackageInstalledQueriesManager fabricates apt-get (the manager) and dpkg
// (its query) on PATH and checks packageInstalled reads the query's exit status
// — and is conservative when the query tool is absent. Linux-only because the
// candidate manager list is keyed on the host OS.
func TestPackageInstalledQueriesManager(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses apt/dpkg fakes")
	}
	bin := t.TempDir()
	writeFakeExec(t, filepath.Join(bin, "apt-get")) // manager detected on PATH
	t.Setenv("PATH", bin)
	spec := config.NewPackage("", map[string]string{"apt": "libnspr4"})

	writeFakeExecCode(t, filepath.Join(bin, "dpkg"), 0) // installed
	if !packageInstalled(spec) {
		t.Fatal("dpkg exit 0 should read as installed")
	}
	writeFakeExecCode(t, filepath.Join(bin, "dpkg"), 1) // not installed
	if packageInstalled(spec) {
		t.Fatal("dpkg exit 1 should read as not installed")
	}
	if err := os.Remove(filepath.Join(bin, "dpkg")); err != nil {
		t.Fatalf("remove dpkg: %v", err)
	}
	if packageInstalled(spec) {
		t.Fatal("no query tool on PATH should read as not installed (conservative)")
	}
}

// TestCheckSatisfiedByInstalledPackage is the end-to-end payoff: a requirement
// that is not an executable on PATH (a shared library) is satisfied when its
// declared package is installed, closing the install-but-never-verify gap.
func TestCheckSatisfiedByInstalledPackage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses apt/dpkg fakes")
	}
	bin := t.TempDir()
	writeFakeExec(t, filepath.Join(bin, "apt-get"))
	writeFakeExecCode(t, filepath.Join(bin, "dpkg"), 0) // package reports installed
	t.Setenv("PATH", bin)

	// libnspr4 is not on PATH (it is a library), but its apt package is present.
	tool := config.Tool{Name: "libnspr4", Package: config.NewPackage("", map[string]string{"apt": "libnspr4"})}
	if err := Check([]config.Tool{tool}, Options{}); err != nil {
		t.Fatalf("an installed package should satisfy a non-executable requirement: %v", err)
	}

	// When the package is not installed, the requirement is unmet.
	writeFakeExecCode(t, filepath.Join(bin, "dpkg"), 1)
	if err := Check([]config.Tool{tool}, Options{}); err == nil {
		t.Fatal("a package that is not installed should leave the requirement unmet")
	}
}

// TestDescribeShowsProviderCommand checks the --install-less error names the
// exact command a provider would run (mise here, which needs no host state).
func TestDescribeShowsProviderCommand(t *testing.T) {
	tool := config.Tool{Name: "dotnet", MinVersion: "10", Mise: "dotnet@10",
		InstallHint: "https://dotnet.microsoft.com/download"}
	got := describe(tool, false, "")
	if !strings.Contains(got, "run with --install to: mise use --global dotnet@10") {
		t.Fatalf("describe should show the mise command, got %q", got)
	}
	if strings.Contains(got, "dotnet.microsoft.com") {
		t.Fatalf("describe should prefer the provider command over installHint, got %q", got)
	}
}

// TestDescribeFallsBackToHint checks a tool with no resolvable provider still
// shows its installHint.
func TestDescribeFallsBackToHint(t *testing.T) {
	tool := config.Tool{Name: "weirdtool", InstallHint: "see docs"}
	got := describe(tool, false, "")
	if !strings.Contains(got, "install: see docs") {
		t.Fatalf("describe should fall back to installHint, got %q", got)
	}
}

func TestPrependPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", "/usr/bin"+sep+"/bin")
	prependPath("/opt/new")
	if got := os.Getenv("PATH"); got != "/opt/new"+sep+"/usr/bin"+sep+"/bin" {
		t.Fatalf("prepend = %q", got)
	}
	// Idempotent: already present → unchanged.
	prependPath("/usr/bin")
	if got := os.Getenv("PATH"); got != "/opt/new"+sep+"/usr/bin"+sep+"/bin" {
		t.Fatalf("prepend should be idempotent, got %q", got)
	}
}

// TestFindMiseLocalFallback checks mise is found in ~/.local/bin even when that
// directory is not on PATH — the case that would otherwise make a fresh
// `tarjan up` fail to see a mise-installed tool.
func TestFindMiseLocalFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake mise + ~/.local/bin layout")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "") // mise is not on PATH, only in ~/.local/bin

	if got := findMise(); got != "" {
		t.Fatalf("no mise anywhere should yield empty, got %q", got)
	}

	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeExec(t, filepath.Join(localBin, "mise"))

	got := findMise()
	if got != filepath.Join(localBin, "mise") {
		t.Fatalf("findMise fallback = %q, want %s", got, filepath.Join(localBin, "mise"))
	}

	// activateMise must put ~/.local/bin on PATH so the shims can exec mise.
	if !activateMise() {
		t.Fatal("activateMise should report mise found")
	}
	if !strings.Contains(os.Getenv("PATH"), localBin) {
		t.Fatalf("activateMise should add %s to PATH, got %q", localBin, os.Getenv("PATH"))
	}
}

// TestInstallWorkdirPins drives the per-workdir pin install with a fake mise
// that records how it was invoked: only a workdir that pins versions triggers
// `mise install`, run inside that workdir.
func TestInstallWorkdirPins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake mise script")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	// A fake mise that appends "<cwd> <args>" per invocation.
	script := fmt.Sprintf("#!/bin/sh\necho \"$(pwd) $*\" >> %s\n", log)
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mise: %v", err)
	}
	t.Setenv("PATH", bin)

	// No pins → no mise invocation.
	unpinned := t.TempDir()
	if err := InstallWorkdirPins(t.Context(), "svc", unpinned); err != nil {
		t.Fatalf("unpinned workdir: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("mise ran for a workdir with no pins")
	}

	// mise.toml pin → `mise install` in that workdir.
	pinned := t.TempDir()
	if err := os.WriteFile(filepath.Join(pinned, "mise.toml"), []byte("[tools]\ndotnet = \"8\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallWorkdirPins(t.Context(), "svc", pinned); err != nil {
		t.Fatalf("pinned workdir: %v", err)
	}
	out, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("mise was not invoked for a pinned workdir: %v", err)
	}
	// macOS: t.TempDir is under /var → /private/var through pwd, so compare by suffix.
	got := strings.TrimSpace(string(out))
	if !strings.HasSuffix(got, filepath.Base(pinned)+" install") {
		t.Fatalf("mise invocation = %q, want `mise install` run in %s", got, pinned)
	}

	// .tool-versions pins too.
	asdf := t.TempDir()
	if err := os.WriteFile(filepath.Join(asdf, ".tool-versions"), []byte("nodejs 20.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallWorkdirPins(t.Context(), "svc", asdf); err != nil {
		t.Fatalf(".tool-versions workdir: %v", err)
	}
	if lines, _ := os.ReadFile(log); strings.Count(string(lines), "\n") != 2 {
		t.Fatalf("expected a second invocation, log:\n%s", lines)
	}
}

// TestInstallWorkdirPinsNoMise checks a pinned workdir without mise anywhere
// warns and continues rather than failing the service's setup.
func TestInstallWorkdirPinsNoMise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX PATH/HOME layout")
	}
	t.Setenv("HOME", t.TempDir()) // no ~/.local/bin/mise fallback either
	t.Setenv("PATH", "")

	pinned := t.TempDir()
	if err := os.WriteFile(filepath.Join(pinned, "mise.toml"), []byte("[tools]\nnode = \"20\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallWorkdirPins(t.Context(), "svc", pinned); err != nil {
		t.Fatalf("missing mise should warn, not fail: %v", err)
	}
}

func writeFakeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake exec: %v", err)
	}
}

// writeFakeExecCode writes a fake executable that exits with the given status —
// used to stand in for a package-manager query (dpkg -s) that reports a package
// present (0) or absent (non-zero).
func writeFakeExecCode(t *testing.T, path string, code int) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nexit %d\n", code)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake exec: %v", err)
	}
}
