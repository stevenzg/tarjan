package cmd

import (
	"context"
	"strings"
	"testing"
	"time"
)

// redirectCache points os.UserCacheDir at a temp dir on both Linux (XDG) and
// macOS (HOME/Library/Caches), so the update-check cache never touches the real
// user cache.
func redirectCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
}

func TestUpdateNoticeDisabled(t *testing.T) {
	t.Setenv("TARJAN_NO_UPDATE_CHECK", "1")
	if got := updateNotice(context.Background()); got != "" {
		t.Fatalf("disabled check should be empty, got %q", got)
	}
}

func TestUpdateNoticeDevBuild(t *testing.T) {
	t.Setenv("TARJAN_NO_UPDATE_CHECK", "")
	prev := version
	t.Cleanup(func() { version = prev })
	version = "dev" // unparseable — never reaches the network

	if got := updateNotice(context.Background()); got != "" {
		t.Fatalf("dev build should be empty, got %q", got)
	}
}

func TestUpdateCachePath(t *testing.T) {
	redirectCache(t)
	p, err := updateCachePath()
	if err != nil {
		t.Fatalf("updateCachePath: %v", err)
	}
	if !strings.Contains(p, "tarjan") || !strings.HasSuffix(p, "update-check.json") {
		t.Fatalf("cache path = %q", p)
	}
}

func TestShouldCheckUpdateThrottle(t *testing.T) {
	redirectCache(t)
	// No cache yet: a check is due.
	if !shouldCheckUpdate() {
		t.Fatal("with no cache, shouldCheckUpdate should be true")
	}
	// After recording, we are inside the throttle window: no check due.
	recordUpdateCheck()
	if shouldCheckUpdate() {
		t.Fatal("just after recording, shouldCheckUpdate should be false")
	}
}

func TestPrintUpdateNotice(t *testing.T) {
	// A message is printed; an empty message prints nothing. Both return promptly.
	msg := make(chan string, 1)
	msg <- "update available"
	printUpdateNotice(msg)

	empty := make(chan string, 1)
	empty <- ""
	printUpdateNotice(empty)
}

func TestStartUpdateNotice(t *testing.T) {
	t.Setenv("TARJAN_NO_UPDATE_CHECK", "1") // resolves to "" without any network
	ch := startUpdateNotice(context.Background())
	select {
	case got := <-ch:
		if got != "" {
			t.Fatalf("disabled check should yield empty, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startUpdateNotice never produced a value")
	}
}
