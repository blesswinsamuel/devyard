package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build-time metadata, overridden via ldflags. For example:
//
//	go build -ldflags "-X github.com/blesswinsamuel/local-compose/internal/cli.Version=v1.2.3 \
//	                   -X github.com/blesswinsamuel/local-compose/internal/cli.Commit=$(git rev-parse --short HEAD) \
//	                   -X github.com/blesswinsamuel/local-compose/internal/cli.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the local-compose version, commit, and build date",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "local-compose version %s\n", Version)
		if Commit != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "commit: %s\n", Commit)
		}
		if Date != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "date: %s\n", Date)
		}
		return nil
	},
}

func init() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("local-compose version {{.Version}}\n")
	rootCmd.AddCommand(versionCmd)
}
