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

		socket, err := ensureDaemon()
		if err != nil {
			return err
		}

		client, err := control.Dial(socket)
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()

		if service == "" {
			// Same resume semantics as `up`: clear markers and start stopped
			// services (or recreate the supervisor if the project is down).
			cfg, loadErr := loadConfig(flagConfigPath, projName)
			if loadErr != nil {
				return loadErr
			}
			if err := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, true); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "local-compose: project %q started\n", projName)
			return nil
		}

		if err := client.Restart(projName, service); err != nil {
			// Project may be stopped (no live supervisor); load & start it.
			cfg, loadErr := loadConfig(flagConfigPath, projName)
			if loadErr == nil {
				if startErr := client.StartProject(cfg.ConfigPath, false, cfg.EnvFile, true); startErr == nil {
					fmt.Fprintf(os.Stderr, "local-compose: started %q\n", service)
					return nil
				}
			}
			return err
		}
		fmt.Fprintf(os.Stderr, "local-compose: started %q\n", service)
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
			// Idempotent: treat unknown/already-stopped as success.
			fmt.Fprintln(os.Stderr, "local-compose: stopped")
			return nil
		}
		fmt.Fprintln(os.Stderr, "local-compose: stopped")
		return nil
	},
}
