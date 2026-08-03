package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the local-compose version. Override at build time with:
//
//	go build -ldflags "-X github.com/blesswinsamuel/local-compose/internal/cli.Version=v1.2.3"
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the local-compose version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.OutOrStdout(), "local-compose version %s\n", Version)
		return nil
	},
}

func init() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("local-compose version {{.Version}}\n")
	rootCmd.AddCommand(versionCmd)
}
