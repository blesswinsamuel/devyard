package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive terminal UI for managing services",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		prog := tui.New(tui.Options{
			Socket:     socket,
			Project:    cfg.Project,
			ConfigPath: cfg.ConfigPath,
		})
		if _, err := prog.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: tui: %v\n", err)
			return err
		}
		return nil
	},
}
