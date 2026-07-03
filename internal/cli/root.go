package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "local-compose",
	Short: "Orchestrate local processes (docker-compose-style, no Docker)",
	Long:  "local-compose is a CLI that orchestrates local processes with a compose-inspired config file.",
}

func init() {
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(tuiCmd)
}
