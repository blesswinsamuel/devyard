package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive terminal UI for managing services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("tui: not implemented")
	},
}
