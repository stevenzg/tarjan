package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/runner"
	"github.com/stevenzg/tarjan/internal/tui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var uiWorkspace string

var uiCmd = &cobra.Command{
	Use:     "ui",
	Aliases: []string{"tui", "dashboard"},
	Short:   "Interactive dashboard for a running environment",
	Long: `ui opens a full-screen dashboard for a running 'tarjan up': live service
status, per-service logs, and keys to restart a service (r) or reload the config
(R). It drives the same control endpoint as 'tarjan restart'/'tarjan reload'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		wsDir, err := workspace.Resolve(cfg, uiWorkspace)
		if err != nil {
			return err
		}
		return tui.Run(wsDir, runner.LogsDir(wsDir))
	},
}

func init() {
	uiCmd.Flags().StringVarP(&uiWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	rootCmd.AddCommand(uiCmd)
}
