package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

var upDetach bool
var upBuild bool

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start services defined in local-compose.yml",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(flagConfigPath, flagProject)
		if err != nil {
			return err
		}
		locs, err := resolveLocations(cfg.Project)
		if err != nil {
			return err
		}
		if err := locs.MkdirAll(); err != nil {
			return err
		}

		if upDetach {
			return upDaemon(cfg, locs)
		}
		return runSupervisor(cfg.ConfigPath, cfg.Project, true, upBuild)
	},
}

// upDaemon spawns the daemonized supervisor (re-exec with setsid) and returns
// once the child has been launched. It refuses to double-start when a live
// pidfile already points at a running supervisor.
func upDaemon(cfg *loadedConfig, locs *project.Locations) error {
	if pid, err := daemon.Running(locs); err != nil {
		return fmt.Errorf("check running supervisor: %w", err)
	} else if pid > 0 {
		return fmt.Errorf("supervisor already running for project %q (pid %d); use `down` first", cfg.Project, pid)
	}

	extra := []string{}
	if upBuild {
		extra = append(extra, "--build")
	}
	pid, err := daemon.Spawn(daemon.Options{
		Locations:  locs,
		Project:    cfg.Project,
		ConfigPath: cfg.ConfigPath,
		ExtraArgs:  extra,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "local-compose: supervisor started (pid %d) for project %q\n", pid, cfg.Project)

	// Wait briefly for the child to bind the control socket so a `ps`
	// immediately after `up -d` doesn't race.
	waitForSocket(locs.Socket, 3*time.Second)
	return nil
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Run supervisor in the background")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "Build services before starting")
}
