package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/selfupdate"
	"github.com/stevenzg/tarjan/internal/ui"
)

var upgradeCheckOnly bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Update tarjan to the latest release",
	Long: `upgrade checks GitHub for the latest tarjan release and, if it is newer than
the running binary, downloads it, verifies its checksum, and replaces this
executable in place.

Use --check to only report whether an update is available without installing it.`,
	Args: cobra.NoArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "report whether an update is available without installing it")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
	defer cancel()

	latest, err := selfupdate.Latest(ctx)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	// A release build that is already at (or ahead of) latest has nothing to do.
	// A dev build (unparseable version) always offers to move to a real release.
	upToDate := selfupdate.Parseable(version) && !selfupdate.IsNewer(version, latest)
	if upToDate {
		ui.Success("tarjan is up to date (%s)", version)
		return nil
	}

	if upgradeCheckOnly {
		ui.Info("a new tarjan is available: %s → %s (run `tarjan upgrade`)", version, latest)
		return nil
	}

	if selfupdate.Parseable(version) {
		ui.Info("upgrading tarjan %s → %s", version, latest)
	} else {
		ui.Info("installing tarjan %s (current build: %s)", latest, version)
	}
	exe, err := selfupdate.Apply(ctx, latest)
	if err != nil {
		return err
	}
	ui.Success("upgraded to %s → %s", latest, exe)
	return nil
}
