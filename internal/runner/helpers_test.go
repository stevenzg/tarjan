package runner

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	if got := backoff(1); got != 500*time.Millisecond {
		t.Fatalf("backoff(1) = %v, want 500ms", got)
	}
	prev := backoff(1)
	for attempt := 2; attempt <= 25; attempt++ {
		d := backoff(attempt)
		if d < prev && d != 10*time.Second {
			t.Fatalf("backoff(%d)=%v decreased below previous %v", attempt, d, prev)
		}
		if d > 10*time.Second || d <= 0 {
			t.Fatalf("backoff(%d) = %v out of range (0, 10s]", attempt, d)
		}
		prev = d
	}
	// Large attempts must saturate at the cap, not overflow to a tiny/negative value.
	if got := backoff(100); got != 10*time.Second {
		t.Fatalf("backoff(100) = %v, want 10s cap", got)
	}
}

func TestPad(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"api", 5, "api  "},
		{"api", 3, "api"},
		{"database", 4, "data"}, // longer than width: truncated
		{"", 2, "  "},
	} {
		if got := pad(tc.in, tc.n); got != tc.want {
			t.Errorf("pad(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestLogPrefixContainsName(t *testing.T) {
	p := logPrefix("api", 0)
	if !strings.Contains(p, "api") {
		t.Errorf("logPrefix = %q, missing service name", p)
	}
	if !strings.Contains(p, "│") {
		t.Errorf("logPrefix = %q, missing column separator", p)
	}
}

func TestDockerImage(t *testing.T) {
	r := New(&config.Config{Name: "prod"}, t.TempDir())

	pull := config.Service{Name: "cache", Docker: &config.DockerSpec{Image: "redis:7"}}
	if got := r.dockerImage(pull); got != "redis:7" {
		t.Errorf("pulled image = %q, want redis:7", got)
	}

	build := config.Service{Name: "api", Docker: &config.DockerSpec{Build: &config.DockerBuild{Context: "api"}}}
	if got := r.dockerImage(build); got != "tarjan-prod-api:dev" {
		t.Errorf("build image = %q, want tarjan-prod-api:dev", got)
	}

	tagged := config.Service{Name: "api", Docker: &config.DockerSpec{
		Image: "myapi:1", Build: &config.DockerBuild{Context: "api"},
	}}
	if got := r.dockerImage(tagged); got != "myapi:1" {
		t.Errorf("build+tag image = %q, want myapi:1", got)
	}
}

func TestDockerRunArgs(t *testing.T) {
	r := New(&config.Config{Name: "prod"}, t.TempDir())
	spec := config.Service{
		Name: "web",
		Docker: &config.DockerSpec{
			Image:   "nginx:latest",
			Ports:   []string{"8080:80"},
			Volumes: []string{"./site:/html"},
			Env:     map[string]string{"FOO": "bar"},
			Args:    []string{"--cpus", "1"},
			Command: []string{"nginx", "-g", "daemon off;"},
		},
	}
	name, args := r.dockerRun(spec, "tarjan-prod-web")
	if name != "docker" {
		t.Fatalf("command = %q, want docker", name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm --name tarjan-prod-web",
		"--label tarjan.env=prod",
		"--label tarjan.service=web",
		"-p 8080:80",
		"-v ./site:/html",
		"-e FOO=bar",
		"--cpus 1",
		"nginx:latest",
		"nginx -g daemon off;",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker args missing %q\nfull: %s", want, joined)
		}
	}
	// Ordering guarantees: run options precede the image, image precedes CMD.
	if strings.Index(joined, "--cpus 1") > strings.Index(joined, "nginx:latest") {
		t.Error("run options must precede the image")
	}
	if strings.Index(joined, "nginx:latest") > strings.Index(joined, "daemon off;") {
		t.Error("image must precede the container command")
	}
}

func TestParseDuration(t *testing.T) {
	if got := parseDuration("", time.Second); got != time.Second {
		t.Errorf("empty -> default: got %v", got)
	}
	if got := parseDuration("garbage", 2*time.Second); got != 2*time.Second {
		t.Errorf("invalid -> default: got %v", got)
	}
	if got := parseDuration("250ms", time.Second); got != 250*time.Millisecond {
		t.Errorf("valid: got %v", got)
	}
}

func TestBuildProbeNilForEmpty(t *testing.T) {
	if buildProbe(&config.Health{}) != nil {
		t.Error("buildProbe(empty health) = non-nil, want nil (no probe)")
	}
}

func TestWaitHealthyNilSpec(t *testing.T) {
	if err := waitHealthy(context.Background(), nil); err != nil {
		t.Errorf("waitHealthy(nil) = %v, want nil", err)
	}
}

func TestWaitHealthyTCPReady(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	h := &config.Health{TCP: ln.Addr().String(), Timeout: "2s", Interval: "20ms"}
	if err := waitHealthy(context.Background(), h); err != nil {
		t.Fatalf("waitHealthy(open port) = %v, want nil", err)
	}
}

func TestWaitHealthyTimesOut(t *testing.T) {
	// Reserve a port, then close it so nothing is listening — dials are refused
	// until the (short) timeout elapses.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := &config.Health{TCP: addr, Timeout: "150ms", Interval: "20ms"}
	if err := waitHealthy(context.Background(), h); err == nil {
		t.Fatal("waitHealthy(closed port) = nil, want timeout error")
	}
}

func TestWaitHealthyHTTP(t *testing.T) {
	// A 2xx response satisfies the probe.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	h := &config.Health{HTTP: ok.URL, Timeout: "2s", Interval: "20ms"}
	if err := waitHealthy(context.Background(), h); err != nil {
		t.Fatalf("waitHealthy(200) = %v, want nil", err)
	}

	// A persistent 5xx never satisfies it: the probe times out.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	h = &config.Health{HTTP: bad.URL, Timeout: "150ms", Interval: "20ms"}
	if err := waitHealthy(context.Background(), h); err == nil {
		t.Fatal("waitHealthy(500) = nil, want timeout error")
	}
}

func TestLatestMod(t *testing.T) {
	// An empty/missing set has a zero latest time.
	if got := latestMod([]string{filepath.Join(t.TempDir(), "nope")}); !got.IsZero() {
		t.Fatalf("latestMod(missing) = %v, want zero", got)
	}

	dir := t.TempDir()
	older := filepath.Join(dir, "a.txt")
	newer := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(older, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force a strictly newer mtime on the second file so the comparison is
	// deterministic regardless of filesystem timestamp granularity.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(newer, future, future); err != nil {
		t.Fatal(err)
	}
	got := latestMod([]string{dir})
	if got.Before(future.Add(-time.Second)) {
		t.Fatalf("latestMod = %v, want ~%v (the newest file)", got, future)
	}
}

func TestWaitHealthyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &config.Health{TCP: "127.0.0.1:9", Timeout: "5s", Interval: "20ms"}
	if err := waitHealthy(ctx, h); err == nil {
		t.Fatal("waitHealthy(canceled ctx) = nil, want error")
	}
}
