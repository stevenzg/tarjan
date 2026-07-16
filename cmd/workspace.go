package cmd

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var (
	wsWorkspace string
	wsOpen      bool
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Aliases: []string{"ws"},
	Short:   "Generate (and optionally open) the VS Code workspace",
	Long: `workspace (re)generates "<name>.code-workspace" for the most recent
workspace, listing every cloned repo as a folder. With --open it launches the
'code' editor on the generated file so all repos open in one window.`,
	Args: cobra.NoArgs,
	RunE: runWorkspace,
}

func init() {
	workspaceCmd.Flags().StringVarP(&wsWorkspace, "workspace", "w", "", "workspace dir (default: most recent)")
	workspaceCmd.Flags().BoolVar(&wsOpen, "open", false, "open the generated workspace in VS Code")
	rootCmd.AddCommand(workspaceCmd)
}

func runWorkspace(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	wsDir, err := workspace.Resolve(cfg, wsWorkspace)
	if err != nil {
		return err
	}
	path, err := workspace.WriteVSCode(cfg, wsDir, cfg.Repos)
	if err != nil {
		return err
	}
	ui.Success("VS Code workspace: %s", path)

	if wsOpen {
		if _, err := exec.LookPath("code"); err != nil {
			ui.Warn("'code' not found on PATH; open %s manually", path)
			return nil
		}
		open := exec.Command("code", path)
		open.Stdout, open.Stderr = os.Stdout, os.Stderr
		if err := open.Run(); err != nil {
			return err
		}
		ui.Success("opened in VS Code")
	}
	return nil
}
