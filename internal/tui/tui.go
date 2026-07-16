// Package tui is an interactive terminal dashboard for a running tarjan
// environment. It is a pure client over the control plane: it polls /status,
// tails per-service log files, and sends restart/reload commands — so it needs
// no changes to the runner.
package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stevenzg/tarjan/internal/control"
)

// Run starts the dashboard for a workspace until the user quits.
func Run(wsDir, logsDir string) error {
	m := model{wsDir: wsDir, logsDir: logsDir, height: 24, width: 80}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type model struct {
	wsDir, logsDir string
	services       []control.ServiceStatus
	cursor         int
	logLines       []string
	width, height  int
	flash          string // transient action feedback
	flashTTL       int    // ticks remaining before flash clears
	connErr        bool   // last /status poll failed (control plane unreachable)
}

// flashTicks is how many 1s ticks a flash message stays visible.
const flashTicks = 4

type (
	tickMsg     time.Time
	statusesMsg struct {
		services []control.ServiceStatus
		err      error
	}
	logsMsg  []string
	flashMsg string
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	readyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	startingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	paneStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchStatuses(m.wsDir), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func fetchStatuses(wsDir string) tea.Cmd {
	return func() tea.Msg {
		s, err := control.Statuses(wsDir)
		return statusesMsg{services: s, err: err}
	}
}

func loadLogs(logsDir, name string, max int) tea.Cmd {
	return func() tea.Msg {
		return logsMsg(tailFile(filepath.Join(logsDir, name+".log"), max))
	}
}

func (m model) selected() (control.ServiceStatus, bool) {
	if m.cursor >= 0 && m.cursor < len(m.services) {
		return m.services[m.cursor], true
	}
	return control.ServiceStatus{}, false
}

func (m model) logHeight() int {
	h := m.height - len(m.services) - 8
	if h < 3 {
		return 3
	}
	return h
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			if s, ok := m.selected(); ok {
				return m, loadLogs(m.logsDir, s.Name, m.logHeight())
			}
		case "down", "j":
			if m.cursor < len(m.services)-1 {
				m.cursor++
			}
			if s, ok := m.selected(); ok {
				return m, loadLogs(m.logsDir, s.Name, m.logHeight())
			}
		case "r":
			if s, ok := m.selected(); ok {
				return m, restartCmd(m.wsDir, s.Name)
			}
		case "R":
			return m, reloadCmd(m.wsDir)
		}

	case tickMsg:
		if m.flashTTL > 0 {
			m.flashTTL--
			if m.flashTTL == 0 {
				m.flash = ""
			}
		}
		return m, tea.Batch(fetchStatuses(m.wsDir), tick())

	case statusesMsg:
		if msg.err != nil {
			// The runner died or the control plane is unreachable — flag it rather
			// than silently freezing on the last snapshot with everything "ready".
			m.connErr = true
			break
		}
		m.connErr = false
		m.services = msg.services
		if m.cursor >= len(m.services) {
			m.cursor = max(0, len(m.services)-1)
		}
		if s, ok := m.selected(); ok {
			return m, loadLogs(m.logsDir, s.Name, m.logHeight())
		}

	case logsMsg:
		m.logLines = []string(msg)

	case flashMsg:
		m.flash = string(msg)
		m.flashTTL = flashTicks
	}
	return m, nil
}

func restartCmd(wsDir, name string) tea.Cmd {
	return func() tea.Msg {
		if err := control.Restart(wsDir, name); err != nil {
			return flashMsg("restart " + name + ": " + err.Error())
		}
		return flashMsg("restarting " + name)
	}
}

func reloadCmd(wsDir string) tea.Cmd {
	return func() tea.Msg {
		if err := control.Reload(wsDir); err != nil {
			return flashMsg("reload: " + err.Error())
		}
		return flashMsg("reload requested")
	}
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tarjan") + dimStyle.Render("  "+m.wsDir) + "\n\n")

	if m.connErr {
		b.WriteString(startingStyle.Render("⚠ control plane unreachable — is `tarjan up` still running?") + "\n\n")
	}

	if len(m.services) == 0 {
		b.WriteString(dimStyle.Render("  no running environment (start one with `tarjan up`)\n"))
		b.WriteString("\n" + footer())
		return b.String()
	}

	for i, s := range m.services {
		cursor := "  "
		name := fmt.Sprintf("%-16s", s.Name)
		if i == m.cursor {
			cursor = selectedStyle.Render("▶ ")
			name = selectedStyle.Render(name)
		}
		b.WriteString(fmt.Sprintf("%s%s %-24s %s\n", cursor, name, kindLabel(s), readyBadge(s.Ready)))
	}

	name := ""
	if s, ok := m.selected(); ok {
		name = s.Name
	}
	logs := strings.Join(m.logLines, "\n")
	if logs == "" {
		logs = dimStyle.Render("(no output yet)")
	}
	b.WriteString("\n" + paneStyle.Width(m.width-4).Render(dimStyle.Render("logs · "+name)+"\n"+logs) + "\n")

	if m.flash != "" {
		b.WriteString(dimStyle.Render("» "+m.flash) + "\n")
	}
	b.WriteString(footer())
	return b.String()
}

func footer() string {
	return dimStyle.Render("↑/↓ select · r restart · R reload · q quit")
}

func kindLabel(s control.ServiceStatus) string {
	switch {
	case s.External:
		return "external"
	case s.Job:
		return "job"
	case s.Docker:
		return "docker"
	default:
		return fmt.Sprintf("pid %d", s.PID)
	}
}

func readyBadge(ready bool) string {
	if ready {
		return readyStyle.Render("● ready")
	}
	return startingStyle.Render("◌ starting")
}

// tailReadBytes bounds how much of a log file's tail is read each poll. Reading
// only the last chunk (rather than the whole file) keeps the once-a-second
// refresh cheap even when a chatty service's log has grown to hundreds of MB.
const tailReadBytes = 64 << 10

// tailFile returns the last max lines of a file (best effort). It reads only the
// final tailReadBytes of the file rather than the whole thing.
func tailFile(path string, max int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := int64(0)
	if fi.Size() > tailReadBytes {
		start = fi.Size() - tailReadBytes
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil
	}
	// When we started mid-file, drop the leading partial line.
	if start > 0 {
		if nl := bytes.IndexByte(buf, '\n'); nl >= 0 {
			buf = buf[nl+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}
