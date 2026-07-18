// Package deps verifies that required external tools are installed and meet
// minimum version constraints before an environment starts.
package deps

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/shellx"
	"github.com/stevenzg/tarjan/internal/ui"
)

// probeTimeout bounds a tool's version command so a probe that hangs (a tool
// that prompts, or a version command that touches the network) cannot block
// `tarjan up` indefinitely.
const probeTimeout = 10 * time.Second

// Options controls how Check handles missing or outdated tools.
type Options struct {
	// AutoInstall runs each tool's resolved install provider (install/mise/
	// package) before failing. It is opt-in: nothing is installed without the
	// caller explicitly setting this.
	AutoInstall bool
	// AI enables an agent-driven last-resort install for tools the deterministic
	// providers can't satisfy, by shelling out to an agent CLI (the Claude CLI by
	// default). It only takes effect together with AutoInstall.
	AI bool
}

// probeConcurrency bounds how many tool version probes run at once. Each probe
// shells out (with a 10s timeout), so probing sequentially makes `up`/`doctor`
// wait on the sum of every tool's probe; a small pool overlaps them.
const probeConcurrency = 8

// probeResult is one tool's evaluation, gathered concurrently before the
// sequential install/report pass (which must stay ordered and serial because
// installs mutate shared state like PATH).
type probeResult struct {
	tool      config.Tool
	path, ver string
	found, ok bool
}

// Check verifies every required tool, optionally installing missing ones.
// Missing required tools (or version mismatches) produce an error; optional
// tools only warn.
func Check(tools []config.Tool, opts Options) error {
	// Make an already-installed mise's shims visible so a mise-managed tool is
	// found on PATH (and inherited by the services that run it) even without
	// --install.
	ensureMisePATH(tools)

	// Probe every tool concurrently (read-only, no shared state), then report
	// and install sequentially in declared order below.
	results := make([]probeResult, len(tools))
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i, t := range tools {
		wg.Add(1)
		go func(i int, t config.Tool) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			path, ver, found, ok := evaluate(t)
			results[i] = probeResult{tool: t, path: path, ver: ver, found: found, ok: ok}
		}(i, t)
	}
	wg.Wait()

	var missing []string
	for _, r := range results {
		t := r.tool
		path, got, found, ok := r.path, r.ver, r.found, r.ok
		if ok {
			report(t, path, got)
			// A tool present but whose version we couldn't parse skips the
			// MinVersion gate — surface that we couldn't verify it rather than
			// implying it passed.
			if t.MinVersion != "" && got == "" {
				ui.Warn("%s: installed but its version could not be determined to verify >= %s", t.Name, t.MinVersion)
			}
			continue
		}

		// Not satisfied. Try to install if opted in and a provider resolves —
		// a bespoke install: command, a mise: spec, or a system package:.
		if opts.AutoInstall {
			if plan := resolveInstall(t); plan != nil {
				if err := plan.run(); err != nil {
					ui.Warn("installing %s failed: %v", t.Name, err)
				} else if p2, v2, f2, ok := evaluate(t); ok {
					ui.Success("installed %s", t.Name)
					report(t, p2, v2)
					continue
				} else {
					// Install ran but the tool still isn't satisfied — describe the
					// post-install state.
					ui.Warn("%s still not satisfied after install", t.Name)
					found, got = f2, v2
				}
			}

			// Last resort: hand the tool to an agent when the deterministic
			// providers couldn't satisfy it and --ai was opted in.
			if opts.AI {
				if err := aiInstall(t); err != nil {
					ui.Warn("ai install of %s failed: %v", t.Name, err)
				} else if p2, v2, f2, ok := evaluate(t); ok {
					ui.Success("installed %s (via %s)", t.Name, aiCLI())
					report(t, p2, v2)
					continue
				} else {
					ui.Warn("%s still not satisfied after ai install", t.Name)
					found, got = f2, v2
				}
			}
		}

		// Reuse the probe results from evaluate rather than re-running the (possibly
		// slow) version subprocess a second time.
		problem := describe(t, found, got)
		if t.Optional {
			ui.Warn("optional tool %s", problem)
			continue
		}
		missing = append(missing, problem)
	}
	if len(missing) > 0 {
		return fmt.Errorf("unmet required tools:\n  - %s", strings.Join(missing, "\n  - "))
	}
	return nil
}

// evaluate probes a tool, returning its path, detected version, whether it was
// found on PATH, and whether it is present and meets any minimum version.
func evaluate(t config.Tool) (path, version string, found, ok bool) {
	// A Check command replaces PATH lookup: it verifies things PATH cannot see
	// (a shared library, a font). Exit 0 means present; there is no path or
	// version to report, and MinVersion does not apply.
	if t.Check != "" {
		if runCheck(t.Check) {
			return "", "", true, true
		}
		return "", "", false, false
	}
	// An executable on PATH is the common case, and the only one that yields a
	// version to gate on MinVersion.
	if path, err := exec.LookPath(t.Name); err == nil {
		version = probeVersion(t)
		if t.MinVersion != "" && version != "" {
			atLeast, comparable := versionAtLeast(version, t.MinVersion)
			if comparable && !atLeast {
				return path, version, true, false
			}
		}
		return path, version, true, true
	}
	// Not on PATH — but a declared system package may still be installed and
	// satisfy the requirement even though it is not an executable (a shared
	// library). Ask the package manager. This removes the asymmetry where a
	// package: could be installed via --install yet never verified, so the tool
	// was reported unsatisfied forever. No version is available this way, so
	// MinVersion is not applied.
	if !t.Package.IsZero() && packageInstalled(t.Package) {
		return "", "", true, true
	}
	return "", "", false, false
}

// runCheck runs a tool's Check command through the OS shell (so pipes and shell
// syntax work) with the same bounded timeout as a version probe, and reports
// whether it exited 0 — the signal that the dependency is present.
func runCheck(command string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	name, args := shellx.Command(command)
	return exec.CommandContext(ctx, name, args...).Run() == nil
}

func report(t config.Tool, path, version string) {
	switch {
	case version != "" && path != "":
		ui.Step("%s %s (%s)", t.Name, version, path)
	case path != "":
		ui.Step("%s (%s)", t.Name, path)
	default:
		// A Check-verified tool has no path or version to show.
		ui.Step("%s", t.Name)
	}
}

// describe explains why a tool is unsatisfied and how to fix it, using the
// probe results already gathered by evaluate (found on PATH, detected version).
// When a provider resolves (install:/mise:/package:) it shows the exact command
// --install would run; otherwise it falls back to the free-text installHint.
func describe(t config.Tool, found bool, version string) string {
	var b strings.Builder
	if !found {
		fmt.Fprintf(&b, "%q not found", t.Name)
	} else {
		fmt.Fprintf(&b, "%q version %s < required %s", t.Name, version, t.MinVersion)
	}
	if plan := resolveInstall(t); plan != nil {
		fmt.Fprintf(&b, " — run with --install to: %s", plan.desc)
	} else if t.InstallHint != "" {
		fmt.Fprintf(&b, " — install: %s", t.InstallHint)
	}
	return b.String()
}

// probeCommand runs name/args with a bounded timeout so a hung version probe
// can't wedge `tarjan up`.
func probeCommand(name string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// probeVersion runs the tool's version command and extracts the first
// dotted-number sequence it finds.
func probeVersion(t config.Tool) string {
	cmdline := t.VersionCommand
	if cmdline == "" {
		cmdline = t.Name + " --version"
	}
	// Shell-parse the command (matching how install commands run) so a
	// versionCommand with quoted arguments survives instead of being split on
	// bare whitespace.
	name, args := shellx.Command(cmdline)
	if name == "" {
		return ""
	}
	out, err := probeCommand(name, args)
	if err != nil && len(out) == 0 {
		return ""
	}
	return extractVersion(string(out))
}

// versionWordRe captures the version after the full word "version" (optionally a
// following "v"), e.g. "version 3.2.1", "version: 2", "version v4". Anchoring to
// the whole word is what keeps a stray "v2" earlier in a banner (e.g.
// "myapp (protocol v2) version 3.4.5") from winning over the real version.
var versionWordRe = regexp.MustCompile(`(?i)\bversion[:=]?\s*v?(\d+(?:\.\d+){0,2})`)

// versionVRe is the next marker: a bare "vN(.N(.N))" token, e.g. "v22.22.2".
var versionVRe = regexp.MustCompile(`(?i)\bv(\d+(?:\.\d+){0,2})\b`)

// versionRe is the fallback when no marker is present: the first dotted
// "major.minor(.patch)" sequence anywhere, so a stray single number in a banner
// is not mistaken for a version.
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// numberRe parses a (possibly bare-major) version such as "20" or "1.2.3",
// used for comparing against a user-supplied minVersion.
var numberRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

func extractVersion(s string) string {
	if m := versionWordRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if m := versionVRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return versionRe.FindString(s)
}

// versionAtLeast reports whether got >= want. The second return value is false
// when the versions could not be compared (callers should treat that as "skip").
func versionAtLeast(got, want string) (ok bool, comparable bool) {
	g := parseVersion(got)
	w := parseVersion(want)
	if g == nil || w == nil {
		return false, false
	}
	for i := 0; i < 3; i++ {
		if g[i] != w[i] {
			return g[i] > w[i], true
		}
	}
	return true, true
}

func parseVersion(s string) []int {
	m := numberRe.FindStringSubmatch(s)
	if m == nil || m[0] == "" {
		return nil
	}
	out := make([]int, 3)
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			out[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return out
}
