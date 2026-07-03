package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Run build commands for services that declare them",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("build: not implemented")
	},
}
