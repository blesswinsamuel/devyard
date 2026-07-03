package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop all services and the supervisor",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("down: not implemented")
	},
}
