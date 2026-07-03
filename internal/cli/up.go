package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var upDetach bool
var upBuild bool

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start services defined in local-compose.yml",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("up: not implemented")
	},
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Run supervisor in the background")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "Build services before starting")
}
