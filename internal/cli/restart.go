package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart [service]",
	Short: "Restart one or all services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("restart: not implemented")
	},
}
