package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/control"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var restartWorkspace string

var restartCmd = &cobra.Command{
	Use:   "restart <service>",
	Short: "Restart one service in a running environment",
	Long: `restart asks a running 'tarjan up' to restart a single service in place —
without tearing down the rest of the environment. Run it from another terminal
while 'tarjan up' is running.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		wsDir, err := workspace.Resolve(cfg, restartWorkspace)
		if err != nil {
			return err
		}
		if err := control.Restart(wsDir, args[0]); err != nil {
			return err
		}
		ui.Success("restarting %s", args[0])
		return nil
	},
}

func init() {
	restartCmd.Flags().StringVarP(&restartWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	rootCmd.AddCommand(restartCmd)
}
