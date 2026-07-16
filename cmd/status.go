package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/control"
	"github.com/stevenzg/tarjan/internal/state"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var (
	statusWorkspace string
	statusWatch     bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the most recent environment's services",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().StringVarP(&statusWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "W", false, "continuously refresh live status until Ctrl+C")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	wsDir, err := workspace.Resolve(cfg, statusWorkspace)
	if err != nil {
		return err
	}
	if statusWatch {
		return watchStatus(wsDir)
	}
	// Prefer the live control endpoint (shows readiness); fall back to the
	// state file when no environment is currently running.
	if live, err := control.Statuses(wsDir); err == nil {
		ui.Info("workspace: %s (live)", wsDir)
		for _, s := range live {
			fmt.Printf("  %-14s %-22s %s\n", s.Name, kindLabel(s), readyLabel(s.Ready))
		}
		return nil
	}

	st, err := state.Load(wsDir)
	if err != nil {
		ui.Warn("no running environment recorded at %s", wsDir)
		return nil
	}

	ui.Info("%s — started %s", st.Name, st.StartedAt.Format("2006-01-02 15:04:05"))
	ui.Info("workspace: %s", wsDir)
	for _, s := range st.Services {
		remote := ""
		if s.Remote != "" {
			remote = "  @" + s.Remote
		}
		switch {
		case s.External:
			fmt.Printf("  %-14s external (cloud/remote)\n", s.Name)
		case s.Job:
			fmt.Printf("  %-14s job     (completed)%s\n", s.Name, remote)
		case s.Docker:
			fmt.Printf("  %-14s docker  %s%s\n", s.Name, s.Container, remote)
		default:
			fmt.Printf("  %-14s pid     %d%s\n", s.Name, s.PID, remote)
		}
	}
	return nil
}

func kindLabel(s control.ServiceStatus) string {
	var label string
	switch {
	case s.External:
		return "external (cloud/remote)"
	case s.Job:
		label = "job"
	case s.Docker:
		label = "docker " + s.Container
	default:
		label = fmt.Sprintf("pid %d", s.PID)
	}
	if s.Remote != "" {
		label += " @" + s.Remote
	}
	return label
}

func readyLabel(ready bool) string {
	if ready {
		return "ready"
	}
	return "starting"
}

// watchStatus repaints the live status table every second until Ctrl+C. It is
// the dependency-free "lite" dashboard (see `tarjan ui` for the full TUI).
func watchStatus(wsDir string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		live, err := control.Statuses(wsDir)
		fmt.Print("\033[H\033[2J") // home + clear
		fmt.Printf("tarjan status — %s  (Ctrl+C to exit)\n\n", time.Now().Format("15:04:05"))
		if err != nil {
			fmt.Println("  no running environment (start one with `tarjan up`)")
		} else {
			for _, s := range live {
				fmt.Printf("  %-14s %-22s %s\n", s.Name, kindLabel(s), readyLabel(s.Ready))
			}
		}
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil
		case <-ticker.C:
		}
	}
}
