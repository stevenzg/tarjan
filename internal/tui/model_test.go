package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stevenzg/tarjan/internal/control"
	"github.com/stevenzg/tarjan/internal/state"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func asModel(t *testing.T, m tea.Model) model {
	t.Helper()
	got, ok := m.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", m)
	}
	return got
}

func demoServices() []control.ServiceStatus {
	return []control.ServiceStatus{
		{Name: "api", Ready: true, PID: 5},
		{Name: "db", Docker: true},
		{Name: "worker"},
	}
}

func TestUpdateWindowSize(t *testing.T) {
	m := model{}
	nm := asModel(t, first(m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})))
	if nm.width != 120 || nm.height != 40 {
		t.Fatalf("size = %dx%d, want 120x40", nm.width, nm.height)
	}
}

func TestUpdateNavigation(t *testing.T) {
	m := model{services: demoServices(), height: 24}

	// down moves the cursor and requests logs for the new selection.
	nm, cmd := m.Update(runes("j"))
	if got := asModel(t, nm).cursor; got != 1 {
		t.Fatalf("after j: cursor = %d, want 1", got)
	}
	if cmd == nil {
		t.Fatal("moving selection should request logs (non-nil cmd)")
	}

	// up at the top stays put.
	top := model{services: demoServices(), cursor: 0}
	if got := asModel(t, first(top.Update(tea.KeyMsg{Type: tea.KeyUp}))).cursor; got != 0 {
		t.Fatalf("up at top: cursor = %d, want 0", got)
	}

	// down at the bottom stays put.
	bottom := model{services: demoServices(), cursor: 2}
	if got := asModel(t, first(bottom.Update(tea.KeyMsg{Type: tea.KeyDown}))).cursor; got != 2 {
		t.Fatalf("down at bottom: cursor = %d, want 2", got)
	}
}

func TestUpdateQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{runes("q"), {Type: tea.KeyCtrlC}} {
		_, cmd := model{}.Update(k)
		if cmd == nil {
			t.Fatalf("key %v should quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("key %v: expected QuitMsg", k)
		}
	}
}

func TestUpdateRestartAndReloadKeys(t *testing.T) {
	m := model{services: demoServices(), cursor: 0, wsDir: t.TempDir()}
	if _, cmd := m.Update(runes("r")); cmd == nil {
		t.Fatal("r should return a restart cmd")
	}
	if _, cmd := m.Update(runes("R")); cmd == nil {
		t.Fatal("R should return a reload cmd")
	}
}

func TestUpdateStatusesMsg(t *testing.T) {
	// A fresh status set populates services and requests logs.
	m := model{height: 24}
	nm, cmd := m.Update(statusesMsg{services: demoServices()})
	if got := asModel(t, nm).services; len(got) != 3 {
		t.Fatalf("services len = %d, want 3", len(got))
	}
	if cmd == nil {
		t.Fatal("statuses with a selection should request logs")
	}

	// An out-of-range cursor is clamped to the last service.
	m2 := model{cursor: 9, height: 24}
	if got := asModel(t, first(m2.Update(statusesMsg{services: demoServices()}))).cursor; got != 2 {
		t.Fatalf("clamped cursor = %d, want 2", got)
	}

	// An error leaves the previous services untouched.
	m3 := model{services: demoServices()}
	if got := asModel(t, first(m3.Update(statusesMsg{err: control.ErrNoServer}))).services; len(got) != 3 {
		t.Fatalf("errored status should keep services, got len %d", len(got))
	}
}

func TestUpdateLogsAndFlash(t *testing.T) {
	nm := asModel(t, first(model{}.Update(logsMsg{"line-1", "line-2"})))
	if len(nm.logLines) != 2 {
		t.Fatalf("logLines = %v, want 2", nm.logLines)
	}
	fm := asModel(t, first(model{}.Update(flashMsg("hi there"))))
	if fm.flash != "hi there" {
		t.Fatalf("flash = %q, want %q", fm.flash, "hi there")
	}
	if fm.flashTTL != flashTicks {
		t.Fatalf("flashTTL = %d, want %d", fm.flashTTL, flashTicks)
	}
}

// TestFlashExpires drives ticks until the flash message clears.
func TestFlashExpires(t *testing.T) {
	m := asModel(t, first(model{wsDir: t.TempDir()}.Update(flashMsg("did a thing"))))
	for i := 0; i < flashTicks; i++ {
		if m.flash == "" {
			t.Fatalf("flash cleared early after %d ticks", i)
		}
		m = asModel(t, first(m.Update(tickMsg(time.Time{}))))
	}
	if m.flash != "" {
		t.Fatalf("flash = %q after %d ticks, want cleared", m.flash, flashTicks)
	}
}

// TestStatusErrorSetsBanner checks a failed /status poll flags the control
// plane as unreachable instead of silently keeping the last snapshot.
func TestStatusErrorSetsBanner(t *testing.T) {
	base := model{services: demoServices()}
	em := asModel(t, first(base.Update(statusesMsg{err: os.ErrClosed})))
	if !em.connErr {
		t.Fatal("connErr should be set after a failed status poll")
	}
	if len(em.services) != len(demoServices()) {
		t.Fatal("last-known services should be preserved on a failed poll")
	}
	if !strings.Contains(em.View(), "unreachable") {
		t.Fatal("view should show the unreachable banner")
	}
	ok := asModel(t, first(em.Update(statusesMsg{services: demoServices()})))
	if ok.connErr {
		t.Fatal("connErr should clear after a successful poll")
	}
}

func TestTailFileLargeSeeksTail(t *testing.T) {
	dir := t.TempDir()

	// Large file: reads only the tail, and drops the leading partial line so no
	// truncated line is ever surfaced.
	big := filepath.Join(dir, "big.log")
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "line-%05d\n", i)
	}
	if err := os.WriteFile(big, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tailFile(big, 5)
	if len(got) != 5 {
		t.Fatalf("tailFile big returned %d lines, want 5", len(got))
	}
	if got[4] != "line-19999" {
		t.Fatalf("tailFile big last line = %q, want line-19999", got[4])
	}
	for _, ln := range got {
		if !strings.HasPrefix(ln, "line-") {
			t.Fatalf("tailFile returned a truncated partial line: %q", ln)
		}
	}

	// Missing file: nil, no panic.
	if got := tailFile(filepath.Join(dir, "nope.log"), 5); got != nil {
		t.Fatalf("tailFile missing = %v, want nil", got)
	}
}

func TestUpdateTickReschedules(t *testing.T) {
	if _, cmd := (model{wsDir: t.TempDir()}).Update(tickMsg(time.Time{})); cmd == nil {
		t.Fatal("tick should re-fetch statuses and re-arm the timer")
	}
}

func TestInitAndTick(t *testing.T) {
	if (model{wsDir: t.TempDir()}).Init() == nil {
		t.Fatal("Init should return a batch command")
	}
	if tick() == nil {
		t.Fatal("tick should return a command")
	}
}

func TestSelected(t *testing.T) {
	if _, ok := (model{}).selected(); ok {
		t.Fatal("no services: selected should be false")
	}
	m := model{services: demoServices(), cursor: 1}
	if s, ok := m.selected(); !ok || s.Name != "db" {
		t.Fatalf("selected = %+v, %v; want db", s, ok)
	}
	if _, ok := (model{services: demoServices(), cursor: 9}).selected(); ok {
		t.Fatal("out-of-range cursor: selected should be false")
	}
}

func TestLogHeight(t *testing.T) {
	if got := (model{height: 0}).logHeight(); got != 3 {
		t.Fatalf("logHeight floor = %d, want 3", got)
	}
	m := model{height: 40, services: make([]control.ServiceStatus, 2)}
	if got := m.logHeight(); got != 40-2-8 {
		t.Fatalf("logHeight = %d, want %d", got, 40-2-8)
	}
}

func TestViewStates(t *testing.T) {
	// No services yet.
	empty := model{wsDir: "/ws", width: 80, height: 24}.View()
	if !strings.Contains(empty, "no running environment") {
		t.Fatalf("empty view missing hint:\n%s", empty)
	}
	if !strings.Contains(empty, "quit") {
		t.Fatalf("view missing footer:\n%s", empty)
	}

	// With services and a flash message.
	m := model{
		wsDir: "/ws", width: 80, height: 24,
		services: demoServices(), cursor: 0,
		logLines: []string{"hello"}, flash: "restarting api",
	}
	v := m.View()
	for _, want := range []string{"api", "db", "worker", "ready", "starting", "hello", "restarting api"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestKindLabelAndBadges(t *testing.T) {
	cases := map[string]control.ServiceStatus{
		"external": {External: true},
		"job":      {Job: true},
		"docker":   {Docker: true},
		"pid 7":    {PID: 7},
	}
	for want, s := range cases {
		if got := kindLabel(s); got != want {
			t.Errorf("kindLabel(%+v) = %q, want %q", s, got, want)
		}
	}
	if !strings.Contains(readyBadge(true), "ready") {
		t.Error("readyBadge(true) should say ready")
	}
	if !strings.Contains(readyBadge(false), "starting") {
		t.Error("readyBadge(false) should say starting")
	}
	if !strings.Contains(footer(), "quit") {
		t.Error("footer should mention quit")
	}
}

// --- control-plane backed commands ---

type fakeEnv struct {
	mu        sync.Mutex
	restarted string
	reloaded  bool
}

func (f *fakeEnv) RestartService(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = name
	return nil
}
func (f *fakeEnv) Status() []state.Service { return []state.Service{{Name: "api", PID: 1}} }
func (f *fakeEnv) IsReady(string) bool     { return true }
func (f *fakeEnv) Reload() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloaded = true
	return nil
}
func (f *fakeEnv) lastRestart() string { f.mu.Lock(); defer f.mu.Unlock(); return f.restarted }
func (f *fakeEnv) didReload() bool     { f.mu.Lock(); defer f.mu.Unlock(); return f.reloaded }

func serveControl(t *testing.T) (string, *fakeEnv) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".tarjan"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := &fakeEnv{}
	srv, err := control.Serve(dir, env)
	if err != nil {
		t.Fatalf("control.Serve: %v", err)
	}
	t.Cleanup(srv.Close)
	return dir, env
}

func TestFetchStatusesCmd(t *testing.T) {
	dir, _ := serveControl(t)
	msg := fetchStatuses(dir)()
	sm, ok := msg.(statusesMsg)
	if !ok {
		t.Fatalf("want statusesMsg, got %T", msg)
	}
	if sm.err != nil {
		t.Fatalf("unexpected err: %v", sm.err)
	}
	if len(sm.services) != 1 || sm.services[0].Name != "api" || !sm.services[0].Ready {
		t.Fatalf("unexpected statuses: %+v", sm.services)
	}
}

func TestFetchStatusesNoServer(t *testing.T) {
	if sm := fetchStatuses(t.TempDir())().(statusesMsg); sm.err == nil {
		t.Fatal("no control server should yield an error")
	}
}

func TestRestartCmd(t *testing.T) {
	dir, env := serveControl(t)
	msg := restartCmd(dir, "api")()
	if fm, ok := msg.(flashMsg); !ok || !strings.Contains(string(fm), "restarting api") {
		t.Fatalf("restart flash = %v", msg)
	}
	if env.lastRestart() != "api" {
		t.Fatalf("env.RestartService not called with api, got %q", env.lastRestart())
	}
}

func TestRestartCmdError(t *testing.T) {
	if fm := restartCmd(t.TempDir(), "api")().(flashMsg); !strings.Contains(string(fm), "restart api:") {
		t.Fatalf("error flash = %q", string(fm))
	}
}

func TestReloadCmd(t *testing.T) {
	dir, env := serveControl(t)
	if fm := reloadCmd(dir)().(flashMsg); !strings.Contains(string(fm), "reload requested") {
		t.Fatalf("reload flash = %q", string(fm))
	}
	if !env.didReload() {
		t.Fatal("env.Reload not called")
	}
}

func TestLoadLogsCmd(t *testing.T) {
	logsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logsDir, "api.log"), []byte("l1\nl2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lm, ok := loadLogs(logsDir, "api", 10)().(logsMsg)
	if !ok {
		t.Fatal("want logsMsg")
	}
	if len(lm) != 2 {
		t.Fatalf("log lines = %v, want 2", []string(lm))
	}
}

// first returns the model half of an Update return pair, for one-line assertions.
func first(m tea.Model, _ tea.Cmd) tea.Model { return m }
