package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Build metadata, injected at release time via -ldflags (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("tarjan %s\n", version)
		fmt.Printf("  commit:  %s\n", commit)
		fmt.Printf("  built:   %s\n", date)
		fmt.Printf("  go:      %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.Version = version
	rootCmd.AddCommand(versionCmd)
}
