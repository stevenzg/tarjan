package ui

import (
	"os"
	"testing"
)

// withColor forces the color-enabled state (both streams) for a test and
// restores it after.
func withColor(t *testing.T, on bool) {
	t.Helper()
	prevOut, prevErr := stdoutEnabled, stderrEnabled
	stdoutEnabled, stderrEnabled = on, on
	t.Cleanup(func() { stdoutEnabled, stderrEnabled = prevOut, prevErr })
}

func TestColorForWrapsPalette(t *testing.T) {
	withColor(t, true)
	for i := 0; i < len(palette)*2+1; i++ {
		if got, want := ColorFor(i), palette[i%len(palette)]; got != want {
			t.Fatalf("ColorFor(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestColorForStableModuloPalette(t *testing.T) {
	withColor(t, true)
	if ColorFor(3) != ColorFor(3+len(palette)) {
		t.Fatal("ColorFor is not stable modulo the palette length")
	}
}

func TestColorDisabledYieldsEmpty(t *testing.T) {
	withColor(t, false)
	if got := ColorFor(0); got != "" {
		t.Errorf("ColorFor with color off = %q, want empty", got)
	}
	if got := Reset(); got != "" {
		t.Errorf("Reset with color off = %q, want empty", got)
	}
}

func TestResetEnabled(t *testing.T) {
	withColor(t, true)
	if got := Reset(); got != reset {
		t.Errorf("Reset = %q, want %q", got, reset)
	}
}

func TestColorEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabledFor(os.Stdout) {
		t.Error("colorEnabledFor(stdout) = true with NO_COLOR set, want false")
	}
	if colorEnabledFor(os.Stderr) {
		t.Error("colorEnabledFor(stderr) = true with NO_COLOR set, want false")
	}
}

// TestPrintersDoNotPanic exercises the status-line helpers so their format
// strings are proven well-formed (a stray %!(EXTRA ...) would still not panic,
// but a nil-deref or bad index would). Output goes to the test's stdout/stderr.
func TestPrintersDoNotPanic(t *testing.T) {
	withColor(t, true) // take the color-emitting branch too
	Info("info %d", 1)
	Step("step %s", "x")
	Success("ok %v", true)
	Warn("warn %d", 2)
	Error("err %s", "e")
	Println("raw line")
}
