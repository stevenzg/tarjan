package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
)

// waitHealthy blocks until the service's health probe passes or the timeout is
// reached. A nil health spec means "no probe" and returns immediately.
func waitHealthy(ctx context.Context, h *config.Health) error {
	if h == nil {
		return nil
	}
	timeout := parseDuration(h.Timeout, 60*time.Second)
	interval := parseDuration(h.Interval, time.Second)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	probe := buildProbe(h)
	if probe == nil {
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Try immediately, then on each tick.
	for {
		if err := probe(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("health check timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}

type probeFunc func(ctx context.Context) error

func buildProbe(h *config.Health) probeFunc {
	switch {
	case h.TCP != "":
		return func(ctx context.Context) error {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", h.TCP)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		}
	case h.HTTP != "":
		client := &http.Client{Timeout: 5 * time.Second}
		return func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.HTTP, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 400 {
				return fmt.Errorf("status %d", resp.StatusCode)
			}
			return nil
		}
	case h.Command != "":
		return func(ctx context.Context) error {
			name, args := shellCommand(h.Command)
			return exec.CommandContext(ctx, name, args...).Run()
		}
	default:
		return nil
	}
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
