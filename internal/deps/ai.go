package deps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/ui"
)

// aiInstallTimeout bounds the agent-driven install so a stuck agent can't wedge
// `tarjan up`.
const aiInstallTimeout = 5 * time.Minute

// aiCLI is the agent CLI tarjan shells out to for the --ai fallback. It defaults
// to the Claude CLI but is overridable so the mechanism isn't wired to one
// binary — tarjan never embeds an agent SDK, it just drives whatever is on PATH.
func aiCLI() string {
	if v := os.Getenv("TARJAN_AI_CLI"); v != "" {
		return v
	}
	return "claude"
}

// aiArgs are the flags passed to the agent CLI before the prompt (which is fed
// on stdin). The default runs the Claude CLI headless with permission to run
// shell commands; override via TARJAN_AI_ARGS (space-separated) for a different
// CLI, model, or permission policy.
func aiArgs() []string {
	if v := os.Getenv("TARJAN_AI_ARGS"); v != "" {
		return strings.Fields(v)
	}
	// --allowedTools is variadic, so keep a single-value flag last; the prompt
	// arrives on stdin, so no trailing positional to be slurped.
	return []string{"-p", "--allowedTools", "Bash", "--permission-mode", "bypassPermissions"}
}

// aiInstall asks the agent CLI to install a tool the deterministic providers
// couldn't — the explicit last resort behind `--install --ai`, for the long
// tail of tools no install:/mise:/package: entry covers.
func aiInstall(t config.Tool) error {
	bin := aiCLI()
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s CLI not found (install it, or set TARJAN_AI_CLI)", bin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aiInstallTimeout)
	defer cancel()
	ui.Info("asking %s to install %s", bin, t.Name)
	cmd := exec.CommandContext(ctx, bin, aiArgs()...)
	cmd.Stdin = strings.NewReader(aiPrompt(t))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// aiPrompt builds the install instruction handed to the agent for a tool.
func aiPrompt(t config.Tool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Install the developer command-line tool %q on this machine so it ends up on PATH. ", t.Name)
	fmt.Fprintf(&b, "The operating system is %s/%s. ", runtime.GOOS, runtime.GOARCH)
	if t.MinVersion != "" {
		fmt.Fprintf(&b, "It must be at least version %s. ", t.MinVersion)
	}
	fmt.Fprintf(&b, "Choose the right method for this OS (the system package manager or the official installer), "+
		"run the commands needed, then verify success by running `%s`. ", verifyCommand(t))
	if t.InstallHint != "" {
		fmt.Fprintf(&b, "Hint: %s. ", t.InstallHint)
	}
	b.WriteString("Install only this tool and its prerequisites; do not change anything else.")
	return b.String()
}

// verifyCommand is the command the agent should run to confirm the tool works.
func verifyCommand(t config.Tool) string {
	if t.VersionCommand != "" {
		return t.VersionCommand
	}
	return t.Name + " --version"
}
