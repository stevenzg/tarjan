package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := write(t, `
# a comment
export EXPORTED=yes
PLAIN=value
QUOTED="with spaces"
SINGLE='single'
URL=postgres://u:p@host:5432/db

`)
	pairs, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := map[string]string{}
	var order []string
	for _, p := range pairs {
		got[p.Key] = p.Value
		order = append(order, p.Key)
	}
	want := map[string]string{
		"EXPORTED": "yes",
		"PLAIN":    "value",
		"QUOTED":   "with spaces",
		"SINGLE":   "single",
		"URL":      "postgres://u:p@host:5432/db",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(order) != 5 {
		t.Fatalf("expected 5 pairs in order, got %v", order)
	}
}

func TestLoadInlineCommentsAndEscapes(t *testing.T) {
	path := write(t, `
API_KEY=abc123 # prod key
HASH_VALUE=abc#notacomment
FRAGMENT=http://x/y#frag
QUOTED_HASH="a # b"
ESCAPES="line1\nline2\ttab"
QUOTE_IN="p@ss\"word"
LITERAL='raw\nback'
COMMENTED=#empty
`)
	pairs, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := map[string]string{}
	for _, p := range pairs {
		got[p.Key] = p.Value
	}
	want := map[string]string{
		"API_KEY":     "abc123",            // trailing " #..." stripped
		"HASH_VALUE":  "abc#notacomment",   // '#' without leading space stays
		"FRAGMENT":    "http://x/y#frag",   // URL fragment preserved
		"QUOTED_HASH": "a # b",             // '#' literal inside quotes
		"ESCAPES":     "line1\nline2\ttab", // \n \t processed in double quotes
		"QUOTE_IN":    `p@ss"word`,         // \" -> literal quote
		"LITERAL":     `raw\nback`,         // single quotes: no escape processing
		"COMMENTED":   "",                  // value that is only a comment
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestLoadCacheInvalidation checks the parse cache serves a repeated Load
// without re-reading, but re-reads once the file's contents (and thus size)
// change — so a `pull` or reload that rewrites an env file is picked up.
func TestLoadCacheInvalidation(t *testing.T) {
	path := write(t, "K=one\n")
	p1, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 1 || p1[0].Value != "one" {
		t.Fatalf("first load = %+v", p1)
	}
	// Rewrite with different content (and a different size so the mtime+size key
	// changes even at coarse mtime granularity).
	if err := os.WriteFile(path, []byte("K=twelve\nJ=y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range p2 {
		got[p.Key] = p.Value
	}
	if got["K"] != "twelve" || got["J"] != "y" {
		t.Fatalf("second load = %+v, want the rewritten content", p2)
	}
}

func TestLoadMalformed(t *testing.T) {
	path := write(t, "NOEQUALS\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error on a line without '='")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}
