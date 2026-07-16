package cmd

import (
	"context"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestRunHooks(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	if err := runHooks(ctx, "test", nil, dir); err != nil {
		t.Fatalf("empty hooks = %v, want nil", err)
	}
	// "exit N" runs through the OS shell on every platform.
	if err := runHooks(ctx, "test", []string{"exit 0"}, dir); err != nil {
		t.Fatalf("passing hook = %v, want nil", err)
	}
	if err := runHooks(ctx, "test", []string{"exit 1"}, dir); err == nil {
		t.Fatal("a failing hook should surface an error")
	}
}

func TestRunPostDown(t *testing.T) {
	dir := t.TempDir()
	// No hooks: a silent no-op.
	runPostDown(&config.Config{}, dir)
	// Passing and failing hooks both run without panicking (failures only warn).
	runPostDown(&config.Config{Hooks: config.Hooks{PostDown: []string{"exit 0"}}}, dir)
	runPostDown(&config.Config{Hooks: config.Hooks{PostDown: []string{"exit 1"}}}, dir)
}

func TestContainerRunning(t *testing.T) {
	// A bogus container name (or a host without docker) must report not-running
	// rather than erroring out.
	if containerRunning("tarjan-does-not-exist-xyz") {
		t.Fatal("bogus container should not be reported as running")
	}
}
