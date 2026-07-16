// Package cmd wires up the tarjan CLI.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/starcfg"
	"github.com/stevenzg/tarjan/internal/ui"
)

// configFlag is the shared --config flag value.
var configFlag string

var rootCmd = &cobra.Command{
	Use:   "tarjan",
	Short: "Spin up a complete local dev environment from code",
	Long: `tarjan brings up a product's entire local development environment from a
single tarjan.yaml: it checks required tools, clones the repos, generates a
VS Code workspace, and starts every service in dependency order.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	err := rootCmd.Execute()
	ui.Flush() // drain buffered stdout before we may os.Exit
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFlag, "config", "c", "", "path to tarjan.yaml (default: ./tarjan.yaml)")
}

// configCandidates are tried, in order, when --config is not given. The
// .tarjan/ variants let `tarjan up` run directly inside a repository that
// carries its own config (see config.RepoConfigDir).
var configCandidates = []string{
	"tarjan.star", "tarjan.yaml", "tarjan.yml",
	".tarjan/tarjan.star", ".tarjan/tarjan.yaml", ".tarjan/tarjan.yml",
}

// resolveConfigPath returns the config file path from --config, or the first of
// tarjan.star / tarjan.yaml / tarjan.yml that exists.
func resolveConfigPath() (string, error) {
	if configFlag != "" {
		if _, err := os.Stat(configFlag); err != nil {
			abs, _ := filepath.Abs(configFlag)
			return "", fmt.Errorf("config not found: %s", abs)
		}
		return configFlag, nil
	}
	for _, c := range configCandidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	abs, _ := filepath.Abs(configCandidates[0])
	return "", fmt.Errorf("no config found near %s (run `tarjan init` to create one)", filepath.Dir(abs))
}

// mergeSelectors combines the --only flag values with positional service
// arguments into a single selection list. `up` and `validate` both let you name
// services either way (and both at once), so they share this so their meaning
// stays identical: `tarjan up studio` == `tarjan up --only studio`.
func mergeSelectors(only, args []string) []string {
	out := make([]string, 0, len(only)+len(args))
	out = append(out, only...)
	out = append(out, args...)
	return out
}

// loadConfig resolves and loads the config, dispatching by extension: .star
// goes through the Starlark loader, everything else is YAML.
func loadConfig() (*config.Config, error) {
	path, err := resolveConfigPath()
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".star") {
		return starcfg.Load(path)
	}
	return config.Load(path)
}
