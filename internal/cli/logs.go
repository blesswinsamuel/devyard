package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Fetch or stream service logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("logs: not implemented")
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
}
