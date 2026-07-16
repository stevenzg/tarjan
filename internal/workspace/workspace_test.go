package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestMaterializeVersionedIsStableAndReused(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "cotalk", WorkspaceRoot: root}
	stamp := time.Date(2026, 6, 30, 14, 15, 0, 0, time.UTC)

	dir, reused, err := Materialize(cfg, "0.1.0", stamp)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	want := filepath.Join(root, "cotalk-0.1.0")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if reused {
		t.Fatal("first Materialize should not report reused")
	}
	if _, err := os.Stat(filepath.Join(dir, ".tarjan")); err != nil {
		t.Fatalf(".tarjan not created: %v", err)
	}

	// Same version → same dir, now reported as reused (and recorded as last).
	dir2, reused2, err := Materialize(cfg, "0.1.0", stamp)
	if err != nil {
		t.Fatalf("Materialize (2nd): %v", err)
	}
	if dir2 != want || !reused2 {
		t.Fatalf("second run = (%q, reused=%v), want (%q, true)", dir2, reused2, want)
	}
	if last, err := getLast(root); err != nil || last != want {
		t.Fatalf("last = (%q, %v), want %q", last, err, want)
	}
}

func TestMaterializeWithoutVersionIsTimestamped(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "cotalk", WorkspaceRoot: root}
	stamp := time.Date(2026, 6, 30, 14, 15, 0, 0, time.UTC)

	dir, reused, err := Materialize(cfg, "", stamp)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if want := filepath.Join(root, "20260630-141500"); dir != want {
		t.Fatalf("dir = %q, want timestamped %q", dir, want)
	}
	if reused {
		t.Fatal("a fresh timestamped dir should not be reused")
	}
}

// TestVersionDirMatchesMaterialize is the contract `tarjan pull <version>`
// relies on: the path VersionDir computes must equal the directory Materialize
// creates for the same version, so pull addresses the workspace up made.
func TestVersionDirMatchesMaterialize(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Name: "edsger", WorkspaceRoot: root}
	stamp := time.Date(2026, 6, 30, 14, 15, 0, 0, time.UTC)

	made, _, err := Materialize(cfg, "feature/x", stamp)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := VersionDir(cfg, "feature/x"); got != made {
		t.Fatalf("VersionDir = %q, want %q (the dir Materialize made)", got, made)
	}
	// The label is sanitized into a single path segment, just like Materialize.
	if got, want := VersionDir(cfg, "feature/x"), filepath.Join(root, "edsger-feature-x"); got != want {
		t.Fatalf("VersionDir = %q, want %q", got, want)
	}
}

func TestSanitizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"0.1.0":        "0.1.0",
		"  v2 ":        "v2",
		"feature/x":    "feature-x",
		"a:b c":        "a-b-c",
		"release\\rc1": "release-rc1",
	} {
		if got := sanitizeVersion(in); got != want {
			t.Errorf("sanitizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
