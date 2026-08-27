package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var runCmd = &cobra.Command{
	Use:   "run <action> [args...]",
	Short: "Run a one-off action defined in local-compose.yml",
	Long:  "Run executes a one-off action command defined under 'actions:' in local-compose.yml, streaming output to stdout.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		actionName := args[0]
		extraArgs := args[1:]

		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		sock, err := ensureDaemon()
		if err != nil {
			return err
		}

		client, err := control.Dial(sock)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()

		if err := client.StartProject(cfg.ConfigPath, false, flagEnvFile, true); err != nil {
			return fmt.Errorf("start project: %w", err)
		}

		exitCode, err := client.RunAction(cmd.Context(), cfg.Project, actionName, extraArgs, func(line string) {
			fmt.Println(line)
		})
		if err != nil {
			return err
		}

		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}
