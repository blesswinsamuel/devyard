package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("ps: not implemented")
	},
}
