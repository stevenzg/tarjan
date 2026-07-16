// Package envfile parses .env files (KEY=VALUE lines) used to feed environment
// variables — and secrets kept out of git — into services.
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Pair is a single KEY=VALUE entry, preserving file order.
type Pair struct {
	Key   string
	Value string
}

// cache memoises parsed env files by path, keyed on (mtime, size) so a changed
// file (e.g. after a `pull`, or between reloads) is re-read while an unchanged
// one — re-requested on every service (re)start — is served without touching
// disk. Callers treat the returned slice as read-only.
var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

type cacheEntry struct {
	mod   time.Time
	size  int64
	pairs []Pair
}

// Load reads a .env file into ordered key/value pairs. Blank lines and
// whole-line #-comments are ignored; a leading "export " is allowed. Values may
// be wrapped in matching single or double quotes; an unquoted value may carry a
// trailing " #comment" (whitespace + hash) which is stripped. Inside double
// quotes the escapes \n \t \r \\ \" are processed and any # is literal; single
// quotes are fully literal. Variable expansion is intentionally not performed.
//
// Results are cached by path and invalidated when the file's mtime or size
// changes, so the same file feeding many services is parsed once per version.
func Load(path string) ([]Pair, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	if e, ok := cache[path]; ok && e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
		cacheMu.Unlock()
		return e.pairs, nil
	}
	cacheMu.Unlock()

	pairs, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	cache[path] = cacheEntry{mod: fi.ModTime(), size: fi.Size(), pairs: pairs}
	cacheMu.Unlock()
	return pairs, nil
}

// parseFile reads and parses the env file at path without consulting the cache.
func parseFile(path string) ([]Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var pairs []Pair
	sc := bufio.NewScanner(f)
	// Allow long lines (e.g. a JSON blob or key material as a value); the default
	// 64 KiB cap would otherwise fail with a cryptic "token too long".
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		eq := strings.IndexByte(text, '=')
		if eq < 0 {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE, got %q", path, line, text)
		}
		key := strings.TrimSpace(text[:eq])
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", path, line)
		}
		pairs = append(pairs, Pair{Key: key, Value: parseValue(text[eq+1:])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return pairs, nil
}

// parseValue turns the raw text after "KEY=" into the final value: it unwraps a
// surrounding quote pair (processing escapes for double quotes) or, for an
// unquoted value, strips a trailing inline comment and surrounding whitespace.
func parseValue(raw string) string {
	raw = strings.TrimLeft(raw, " \t")
	if raw == "" {
		return ""
	}
	switch raw[0] {
	case '"':
		if v, ok := scanQuoted(raw, '"', true); ok {
			return v
		}
	case '\'':
		if v, ok := scanQuoted(raw, '\'', false); ok {
			return v
		}
	}
	// Unquoted: an inline comment begins at the first '#' preceded by whitespace
	// (or at the very start), so a '#' inside a value like a URL fragment stays.
	if i := inlineCommentIndex(raw); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimRight(raw, " \t")
}

// scanQuoted reads a value that opens with s[0]==quote. For double quotes it
// processes \n \t \r \\ \" escapes; single quotes are literal. Text after the
// closing quote (e.g. a trailing comment) is ignored. It returns false when no
// closing quote is found, so the caller can fall back to literal handling.
func scanQuoted(s string, quote byte, expandEscapes bool) (string, bool) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if expandEscapes && c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		if c == quote {
			return b.String(), true
		}
		b.WriteByte(c)
	}
	return "", false
}

// inlineCommentIndex returns the index of an unquoted inline comment's '#', or
// -1. The '#' must sit at the start or just after whitespace so it isn't
// confused with a '#' that is part of the value itself.
func inlineCommentIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return i
		}
	}
	return -1
}
