package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/deps"
	"github.com/stevenzg/tarjan/internal/ui"
)

var (
	doctorInstall bool
	doctorAI      bool
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that required tools are installed (optionally install them)",
	Long: `doctor verifies every tool in 'requires' is present and meets its minimum
version. With --install it installs each missing/outdated tool via its provider
(install/mise/package). Add --ai to let an agent CLI install what the providers
can't. Use this to get a machine ready before the first 'tarjan up'.

Name one or more services to scope the check to the toolchain those services
need (e.g. "tarjan doctor studio-cloud") — a tool tagged with 'services:' is
reported only when one of its services is in the selection; untagged baseline
tools are always checked. With no arguments, every required tool is checked.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if len(cfg.Requires) == 0 {
			ui.Info("no required tools declared")
			return nil
		}
		if doctorAI && !doctorInstall {
			ui.Warn("--ai has no effect without --install")
		}
		// Scope to named services when given, mirroring `tarjan up <service>`;
		// with no names, check the full baseline (every required tool).
		tools := cfg.Requires
		if len(args) > 0 {
			sel, err := cfg.SelectServices(args, nil, true)
			if err != nil {
				return err
			}
			tools = cfg.RequiredTools(sel)
			if len(tools) == 0 {
				ui.Info("no required tools for the selected services")
				return nil
			}
		}
		ui.Info("checking required tools")
		if err := deps.Check(tools, deps.Options{AutoInstall: doctorInstall, AI: doctorAI}); err != nil {
			return err
		}
		ui.Success("all required tools satisfied")
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorInstall, "install", false, "install missing/outdated tools via their provider (install/mise/package)")
	doctorCmd.Flags().BoolVar(&doctorAI, "ai", false, "let an agent CLI install tools the providers can't (with --install)")
	rootCmd.AddCommand(doctorCmd)
}
