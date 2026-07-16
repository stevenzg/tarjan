package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/runner"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var (
	logsWorkspace string
	logsFollow    bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Print captured logs for a service",
	Long: `logs prints the captured output of a service from the most recent
workspace. With no service it lists which services have logs. Use -f to follow.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().StringVarP(&logsWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "follow the log as it grows")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	wsDir, err := workspace.Resolve(cfg, logsWorkspace)
	if err != nil {
		return err
	}
	logsDir := runner.LogsDir(wsDir)

	if len(args) == 0 {
		return listLogs(logsDir)
	}

	path := filepath.Join(logsDir, args[0]+".log")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no logs for %q (try `tarjan logs` to list)", args[0])
	}
	if logsFollow {
		return followLog(path)
	}
	return dumpLog(path)
}

func listLogs(logsDir string) error {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		ui.Warn("no logs found at %s", logsDir)
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			names = append(names, strings.TrimSuffix(e.Name(), ".log"))
		}
	}
	if len(names) == 0 {
		ui.Warn("no logs found at %s", logsDir)
		return nil
	}
	sort.Strings(names)
	ui.Info("services with logs (use `tarjan logs <service>`):")
	for _, n := range names {
		fmt.Printf("  %s\n", n)
	}
	return nil
}

func dumpLog(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(os.Stdout, f)
	return err
}

// followLog prints the file then tails it, polling for growth until Ctrl+C.
func followLog(path string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(os.Stdout, f); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = os.Stdout.Write(buf[:n])
			continue
		}
		if err != nil && err != io.EOF {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}
