package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build-time metadata, overridden via ldflags.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func newVersionCmd(ctx *CLIContext) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the local-compose version, commit, and build date",
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx.IsJSON() {
				return ctx.PrintJSON(map[string]string{
					"version": Version,
					"commit":  Commit,
					"date":    Date,
				})
			}
			_, _ = fmt.Fprintf(ctx.Out, "local-compose version %s\n", Version)
			if Commit != "" {
				_, _ = fmt.Fprintf(ctx.Out, "commit: %s\n", Commit)
			}
			if Date != "" {
				_, _ = fmt.Fprintf(ctx.Out, "date: %s\n", Date)
			}
			return nil
		},
	}
}
