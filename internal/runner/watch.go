package runner

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"
)

// startWatcher polls the service's watched paths and emits on the returned
// channel when a modification settles (after the debounce window). It is
// poll-based on purpose: no third-party fsnotify dependency, which keeps tarjan a
// single self-contained binary. For the handful of source dirs a service
// watches, a periodic mtime scan is more than fast enough.
//
// It returns the change channel and a stop function that must be called.
func (s *supervisor) startWatcher(ctx context.Context) (<-chan struct{}, func()) {
	changes := make(chan struct{}, 1)
	wctx, cancel := context.WithCancel(ctx)

	debounce := parseDuration(s.spec.Watch.Debounce, 300*time.Millisecond)
	const pollInterval = 500 * time.Millisecond

	roots := make([]string, 0, len(s.spec.Watch.Paths))
	base := s.runner.serviceDir(s.spec)
	for _, p := range s.spec.Watch.Paths {
		roots = append(roots, filepath.Join(base, p))
	}

	go func() {
		last := latestMod(roots)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case <-ticker.C:
				cur := latestMod(roots)
				if cur.After(last) {
					// Wait for changes to settle, then emit a single event.
					if !settle(wctx, roots, cur, debounce, pollInterval) {
						return
					}
					last = latestMod(roots)
					select {
					case changes <- struct{}{}:
					case <-wctx.Done():
						return
					}
				}
			}
		}
	}()

	return changes, cancel
}

// settle waits until the watched paths stop changing for the debounce window.
// Returns false if the context is cancelled.
func settle(ctx context.Context, roots []string, seen time.Time, debounce, poll time.Duration) bool {
	timer := time.NewTimer(debounce)
	defer timer.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if cur := latestMod(roots); cur.After(seen) {
				seen = cur
				timer.Reset(debounce)
			}
		case <-timer.C:
			return true
		}
	}
}

// prunedDirs are directories skipped when walking watched paths: they hold
// dependencies or build/VCS output, are frequently enormous, and are not source
// a developer edits — walking (and stat-ing every file under) them each poll is
// pure overhead that can dominate the scan on a real project.
var prunedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"bin":          true,
	"obj":          true,
	".next":        true,
	".idea":        true,
}

// latestMod returns the most recent modification time across all roots,
// walking directories recursively but pruning heavy non-source directories.
// Missing paths are ignored.
func latestMod(roots []string) time.Time {
	var latest time.Time
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() && path != root && prunedDirs[d.Name()] {
				return fs.SkipDir
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
			return nil
		})
	}
	return latest
}
