package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/gitutil"
	"github.com/stevenzg/tarjan/internal/ui"
	"github.com/stevenzg/tarjan/internal/workspace"
)

var pullWorkspace string

var pullCmd = &cobra.Command{
	Use:   "pull [version]",
	Short: "Update the cloned repos of a workspace with git pull",
	Long: `pull fast-forwards every repo the config clones (` + "`git pull --ff-only`" + `),
so one command brings all of a product's checkouts up to date.

By default it targets the most recently started workspace. Pass a version to
target a named, reusable workspace (` + "`<name>-<version>`" + `, the same directory
` + "`tarjan up --version <version>`" + ` uses), or --workspace to point at an explicit
directory:

  tarjan pull            # update the most recent workspace
  tarjan pull dev        # update the "<name>-dev" workspace
  tarjan pull -w ./ws    # update an explicit workspace directory

Repos are pulled fast-forward-only, so local commits are never rewritten or
merged; a checkout that can't fast-forward is reported and left untouched. A
repo that hasn't been cloned yet is skipped — run "tarjan up" to clone it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPull,
}

func init() {
	pullCmd.Flags().StringVarP(&pullWorkspace, "workspace", "w", "", "workspace dir to update (default: most recent, or the named <name>-<version>)")
	rootCmd.AddCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Resolve the workspace whose checkouts we update: an explicit --workspace
	// wins; a positional version names the reusable "<name>-<version>" dir;
	// otherwise fall back to the most recent (or in-place) workspace.
	var wsDir string
	switch {
	case pullWorkspace != "":
		wsDir = pullWorkspace
	case len(args) > 0 && args[0] != "":
		wsDir = workspace.VersionDir(cfg, args[0])
		if _, err := os.Stat(wsDir); err != nil {
			return fmt.Errorf("no workspace for version %q at %s (run `tarjan up --version %s` first)", args[0], wsDir, args[0])
		}
	default:
		wsDir, err = workspace.Resolve(cfg, "")
		if err != nil {
			return err
		}
	}

	repos := cfg.Repos
	if len(repos) == 0 {
		ui.Info("no repos configured to pull")
		return nil
	}

	// Ctrl+C cancels an in-flight fetch.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ui.Info("updating %d repo(s) in %s", len(repos), wsDir)
	if err := gitutil.PullAll(ctx, wsDir, repos); err != nil {
		return err
	}
	ui.Success("repos up to date")
	return nil
}
