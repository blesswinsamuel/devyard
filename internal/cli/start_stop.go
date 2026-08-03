package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/control"
)

var startCmd = &cobra.Command{
	Use:   "start [service]",
	Short: "Start or resume one service, or all services",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName, err := resolveProjectName(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		service := ""
		if len(args) == 1 {
			service = args[0]
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running (is it up?)\n")
			return err
		}
		defer func() { _ = client.Close() }()

		if service == "" {
			if err := client.Restart(projName, ""); err != nil {
				// If project is stopped/not loaded in memory, try loading & starting it via StartProject
				cfg, loadErr := loadConfig(flagConfigPath, projName)
				if loadErr == nil {
					if startErr := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile); startErr == nil {
						fmt.Fprintf(os.Stderr, "local-compose: project %q started\n", projName)
						return nil
					}
				}
				return err
			}
			fmt.Fprintln(os.Stderr, "local-compose: started all services")
		} else {
			if err := client.Restart(projName, service); err != nil {
				cfg, loadErr := loadConfig(flagConfigPath, projName)
				if loadErr == nil {
					if startErr := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile); startErr == nil {
						fmt.Fprintf(os.Stderr, "local-compose: started %q\n", service)
						return nil
					}
				}
				return err
			}
			fmt.Fprintf(os.Stderr, "local-compose: started %q\n", service)
		}
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [service]",
	Short: "Stop one service, or all services in the project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projName, err := resolveProjectName(flagConfigPath, flagProject)
		if err != nil {
			return err
		}

		socket, err := dialDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}

		client, err := control.Dial(socket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
			return nil
		}
		defer func() { _ = client.Close() }()

		if len(args) == 1 {
			service := args[0]
			if err := client.StopService(projName, service); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "local-compose: stopped %q\n", service)
			return nil
		}

		if err := client.StopProject(projName); err != nil {
			fmt.Fprintln(os.Stderr, "local-compose: stopped")
			return nil
		}
		fmt.Fprintln(os.Stderr, "local-compose: stopped")
		return nil
	},
}
