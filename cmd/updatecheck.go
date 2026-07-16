package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stevenzg/tarjan/internal/selfupdate"
	"github.com/stevenzg/tarjan/internal/ui"
)

// updateCheckInterval throttles the background "update available" probe so a
// repeated `tarjan up` doesn't hit the GitHub API on every start.
const updateCheckInterval = 24 * time.Hour

// startUpdateNotice kicks off a background check for a newer release and returns
// a channel that yields a one-line notice (empty when none is due: disabled,
// throttled, offline, a dev build, or already current). It never blocks the run
// or surfaces an error — a failed check simply stays quiet.
func startUpdateNotice(ctx context.Context) <-chan string {
	ch := make(chan string, 1)
	go func() { ch <- updateNotice(ctx) }()
	return ch
}

// printUpdateNotice prints the notice once it arrives, giving up after a short
// grace so a slow network never holds up the "environment is up" prompt.
func printUpdateNotice(ch <-chan string) {
	select {
	case msg := <-ch:
		if msg != "" {
			ui.Info("%s", msg)
		}
	case <-time.After(2 * time.Second):
	}
}

func updateNotice(ctx context.Context) string {
	if os.Getenv("TARJAN_NO_UPDATE_CHECK") != "" {
		return ""
	}
	if !selfupdate.Parseable(version) { // dev build — nothing to compare against
		return ""
	}
	if !shouldCheckUpdate() {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	latest, err := selfupdate.Latest(cctx)
	if err != nil {
		return "" // offline or rate-limited — stay quiet
	}
	recordUpdateCheck()
	if selfupdate.IsNewer(version, latest) {
		return fmt.Sprintf("a new tarjan is available: %s → %s (run `tarjan upgrade`)", version, latest)
	}
	return ""
}

func updateCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tarjan", "update-check.json"), nil
}

type updateCheckCache struct {
	CheckedAt time.Time `json:"checkedAt"`
}

// shouldCheckUpdate reports whether the throttle window has elapsed since the
// last check (or there was none).
func shouldCheckUpdate() bool {
	path, err := updateCachePath()
	if err != nil {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var c updateCheckCache
	if json.Unmarshal(data, &c) != nil {
		return true
	}
	return time.Since(c.CheckedAt) >= updateCheckInterval
}

func recordUpdateCheck() {
	path, err := updateCachePath()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.Marshal(updateCheckCache{CheckedAt: time.Now()})
	_ = os.WriteFile(path, data, 0o644)
}
