package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.log")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := tailFile(path, 3)
	want := []string{"c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Fewer lines than requested returns them all; missing file returns nil.
	if all := tailFile(path, 100); len(all) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(all))
	}
	if nope := tailFile(filepath.Join(dir, "missing.log"), 3); nope != nil {
		t.Fatalf("missing file should return nil, got %v", nope)
	}
}
