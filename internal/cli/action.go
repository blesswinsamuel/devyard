package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var actionCmd = &cobra.Command{
	Use:   "action",
	Short: "Manage and run project actions",
}

var actionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List defined actions in the project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		sock, err := dialDaemon()
		if err != nil {
			if len(cfg.File.Actions) == 0 {
				fmt.Println("No actions defined.")
				return nil
			}
			for name, act := range cfg.File.Actions {
				fmt.Printf("%-15s %s\n", name, act.Spec.Command)
			}
			return nil
		}

		client, err := control.Dial(sock)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()

		actions, err := client.ListActions(cfg.Project)
		if err != nil {
			return err
		}

		if len(actions) == 0 {
			fmt.Println("No actions defined.")
			return nil
		}

		for _, act := range actions {
			fmt.Printf("%-15s %s\n", act.Name, act.Command)
		}
		return nil
	},
}

var actionRunCmd = &cobra.Command{
	Use:   "run <action> [args...]",
	Short: "Run a defined action (alias for 'local-compose run')",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runCmd.RunE,
}

var actionStopCmd = &cobra.Command{
	Use:   "stop <action>",
	Short: "Stop a running action",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		actionName := args[0]
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		sock, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}

		client, err := control.Dial(sock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}
		defer func() { _ = client.Close() }()

		if err := client.StopAction(cfg.Project, actionName); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "local-compose: stopped action %q\n", actionName)
		return nil
	},
}

func init() {
	actionCmd.AddCommand(actionListCmd, actionRunCmd, actionStopCmd)
}
