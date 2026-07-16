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

func TestAIPrompt(t *testing.T) {
	tool := config.Tool{
		Name: "dotnet", MinVersion: "10",
		VersionCommand: "dotnet --version",
		InstallHint:    "https://dotnet.microsoft.com/download",
	}
	got := aiPrompt(tool)
	for _, want := range []string{`"dotnet"`, "version 10", "dotnet --version", runtime.GOOS, "https://dotnet.microsoft.com/download"} {
		if !strings.Contains(got, want) {
			t.Errorf("aiPrompt missing %q in:\n%s", want, got)
		}
	}
}

func TestAICLIOverride(t *testing.T) {
	if aiCLI() != "claude" {
		t.Fatalf("default aiCLI = %q, want claude", aiCLI())
	}
	t.Setenv("TARJAN_AI_CLI", "myagent")
	if aiCLI() != "myagent" {
		t.Fatalf("override aiCLI = %q", aiCLI())
	}
}

func TestAIArgsOverride(t *testing.T) {
	def := aiArgs()
	if len(def) == 0 || def[0] != "-p" {
		t.Fatalf("default aiArgs = %v, want to start with -p", def)
	}
	t.Setenv("TARJAN_AI_ARGS", "--print --model sonnet")
	if got := aiArgs(); len(got) != 3 || got[0] != "--print" || got[2] != "sonnet" {
		t.Fatalf("override aiArgs = %v", got)
	}
}

// TestAIFallbackInstalls drives the whole --ai path with a fake agent CLI that
// materialises the tool onto PATH — proving Check reaches and honours the AI
// fallback when the deterministic providers resolve nothing.
func TestAIFallbackInstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake-agent script")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A fake agent CLI that "installs" the tool by dropping it on PATH. It
	// ignores its args and stdin prompt, exactly like a real install would be
	// opaque to tarjan.
	agent := filepath.Join(bin, "fakeagent")
	script := fmt.Sprintf("#!/bin/sh\nprintf '#!/bin/sh\\necho 9.9.9\\n' > %s/tarjanaitool\nchmod +x %s/tarjanaitool\n", bin, bin)
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	t.Setenv("TARJAN_AI_CLI", agent)

	tool := config.Tool{Name: "tarjanaitool", VersionCommand: "tarjanaitool"}

	// --install alone can't help: no install/mise/package provider is configured.
	if err := Check([]config.Tool{tool}, Options{AutoInstall: true}); err == nil {
		t.Fatal("expected failure with no provider and AI off")
	}
	// --install --ai lets the agent install it.
	if err := Check([]config.Tool{tool}, Options{AutoInstall: true, AI: true}); err != nil {
		t.Fatalf("ai fallback should satisfy the tool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "tarjanaitool")); err != nil {
		t.Fatalf("ai fallback did not create the tool: %v", err)
	}
}

// TestAIRequiresAutoInstall checks the agent is not invoked without AutoInstall,
// even if AI is set — installation stays behind the single --install gate.
func TestAIRequiresAutoInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake-agent script")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A fake agent that would create the tool if ever called.
	agent := filepath.Join(bin, "fakeagent")
	script := fmt.Sprintf("#!/bin/sh\nprintf '#!/bin/sh\\necho 1\\n' > %s/shouldnotexist\nchmod +x %s/shouldnotexist\n", bin, bin)
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	t.Setenv("TARJAN_AI_CLI", agent)

	tool := config.Tool{Name: "tarjanabsent", VersionCommand: "tarjanabsent"}
	if err := Check([]config.Tool{tool}, Options{AI: true}); err == nil {
		t.Fatal("expected failure: AI without AutoInstall must not install")
	}
	if _, err := os.Stat(filepath.Join(bin, "shouldnotexist")); err == nil {
		t.Fatal("agent ran without AutoInstall — install gate leaked")
	}
}
