package cmd

import (
	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/ui"
)

var (
	validateOnly     []string
	validateProfiles []string
	validateNoDeps   bool
)

var validateCmd = &cobra.Command{
	Use:   "validate [service...]",
	Short: "Parse and validate the config; preview what a selection would run",
	Long: `validate parses and checks the config. Named services (or --only/--profile)
also print exactly which services and repos that selection would start, so you
can preview a subset — e.g. "tarjan validate studio" — before running it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		ui.Success("%s is valid", cfg.Name)

		// Positional args select services like --only (and combine with it).
		only := mergeSelectors(validateOnly, args)

		services, err := cfg.SelectServices(only, validateProfiles, !validateNoDeps)
		if err != nil {
			return err
		}
		repos := cfg.SelectRepos(validateProfiles)

		if len(only) == 0 && len(validateProfiles) == 0 {
			ui.Info("%d repo(s), %d service(s)", len(cfg.Repos), len(cfg.Services))
		} else {
			ui.Info("selection → %d/%d repo(s), %d/%d service(s)",
				len(repos), len(cfg.Repos), len(services), len(cfg.Services))
		}
		if len(services) > 0 {
			ui.Info("start order: %v", serviceNames(services))
		}
		return nil
	},
}

func serviceNames(s []config.Service) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}

func init() {
	validateCmd.Flags().StringSliceVar(&validateOnly, "only", nil, "preview starting only these services")
	validateCmd.Flags().StringSliceVar(&validateProfiles, "profile", nil, "preview with these profiles active")
	validateCmd.Flags().BoolVar(&validateNoDeps, "no-deps", false, "do not pull in dependencies")
	rootCmd.AddCommand(validateCmd)
}
