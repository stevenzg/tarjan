package workspace

import (
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestResolveInPlace(t *testing.T) {
	repoDir := t.TempDir()
	cfg := &config.Config{Name: "myrepo", WorkspaceRoot: t.TempDir()}
	cfg.SetInPlaceDir(repoDir)

	got, err := Resolve(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != repoDir {
		t.Fatalf("Resolve = %q, want the repo checkout %q", got, repoDir)
	}
	// An explicit workspace still wins.
	if got, err = Resolve(cfg, "/elsewhere"); err != nil || got != "/elsewhere" {
		t.Fatalf("Resolve explicit = %q, %v", got, err)
	}
}
