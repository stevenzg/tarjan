package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/control"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var reloadWorkspace string

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reconcile a running environment to the current config",
	Long: `reload tells a running 'tarjan up' to re-read tarjan.yaml and reconcile:
added services start, removed services stop, and changed services restart with
their new spec — without bringing the whole environment down.

The config is validated before anything changes; the reconcile then proceeds in
the running 'tarjan up' (watch its output).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		wsDir, err := workspace.Resolve(cfg, reloadWorkspace)
		if err != nil {
			return err
		}
		if err := control.Reload(wsDir); err != nil {
			return err
		}
		ui.Success("reload requested")
		return nil
	},
}

func init() {
	reloadCmd.Flags().StringVarP(&reloadWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	rootCmd.AddCommand(reloadCmd)
}
