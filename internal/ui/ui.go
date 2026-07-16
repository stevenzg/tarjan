// Package ui provides small terminal helpers: colored log prefixes and status
// messages. Colors are disabled automatically when output is not a TTY or when
// NO_COLOR is set.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	mu sync.Mutex
	// out buffers stdout so a high-volume run — every log line of every service
	// streams through Println — coalesces into far fewer write syscalls than one
	// per line. Status lines (Info/Step/Success) flush immediately so interactive
	// feedback stays prompt; a background flusher bounds how long a trickle of
	// pure log output waits, and Flush is called on exit.
	out         = bufio.NewWriterSize(os.Stdout, 32*1024)
	flusherOnce sync.Once
	// Color is decided per stream: `tarjan up 2>errors.log` must not write ANSI
	// escapes into the redirected stderr just because stdout is still a TTY, and
	// `tarjan up | tee` must not drop colors from stderr warnings that are still
	// going to the terminal.
	stdoutEnabled = colorEnabledFor(os.Stdout)
	stderrEnabled = colorEnabledFor(os.Stderr)
)

// flushInterval bounds how long buffered stdout waits before reaching the
// terminal when output has gone quiet (a burst larger than the buffer flushes
// on its own; this covers the idle trickle).
const flushInterval = 200 * time.Millisecond

// startFlusher lazily starts the background flusher on first buffered write.
func startFlusher() {
	flusherOnce.Do(func() {
		go func() {
			t := time.NewTicker(flushInterval)
			defer t.Stop()
			for range t.C {
				mu.Lock()
				_ = out.Flush()
				mu.Unlock()
			}
		}()
	})
}

// Flush drains any buffered stdout. Call it on exit paths (including before
// os.Exit) so the final lines are not lost with the process.
func Flush() {
	mu.Lock()
	defer mu.Unlock()
	_ = out.Flush()
}

// ANSI color codes used for per-service log prefixes.
var palette = []string{
	"\033[36m", // cyan
	"\033[32m", // green
	"\033[35m", // magenta
	"\033[33m", // yellow
	"\033[34m", // blue
	"\033[31m", // red
	"\033[96m", // bright cyan
	"\033[92m", // bright green
}

const (
	reset = "\033[0m"
	dim   = "\033[2m"
	bold  = "\033[1m"
)

func colorEnabledFor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// color gates a code on stdout's color setting (used by the stdout helpers and
// the per-service log prefixes, which are written to stdout).
func color(code string) string {
	if !stdoutEnabled {
		return ""
	}
	return code
}

// colorErr gates a code on stderr's color setting (used by Warn/Error).
func colorErr(code string) string {
	if !stderrEnabled {
		return ""
	}
	return code
}

// ColorFor returns a stable color code for index i.
func ColorFor(i int) string { return color(palette[i%len(palette)]) }

// Reset returns the reset code (empty when colors are disabled).
func Reset() string { return color(reset) }

// Info prints an informational line.
func Info(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(out, color(bold)+"›"+color(reset)+" "+format+"\n", a...)
	_ = out.Flush()
}

// Step prints a sub-step line.
func Step(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(out, color(dim)+"  • "+format+color(reset)+"\n", a...)
	_ = out.Flush()
}

// Success prints a success line.
func Success(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(out, color("\033[32m")+"✓"+color(reset)+" "+format+"\n", a...)
	_ = out.Flush()
}

// Warn prints a warning line. Buffered stdout is flushed first so the warning
// (on stderr) stays ordered after any log output that preceded it.
func Warn(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	_ = out.Flush()
	fmt.Fprintf(os.Stderr, colorErr("\033[33m")+"!"+colorErr(reset)+" "+format+"\n", a...)
}

// Error prints an error line.
func Error(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	_ = out.Flush()
	fmt.Fprintf(os.Stderr, colorErr("\033[31m")+"✗"+colorErr(reset)+" "+format+"\n", a...)
}

// Println writes a raw line to buffered stdout (used by log streamers). It does
// not flush per line; the buffer flushes when full, on the next status line, on
// the background ticker, and on Flush.
func Println(s string) {
	startFlusher()
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(out, s)
}
