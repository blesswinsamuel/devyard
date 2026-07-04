package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/supervisor"
)

// runSupervisor is the foreground supervisor entry point. It is used directly
// by `local-compose up` (foreground) and by the daemonized re-exec child
// (argv `--supervisor <project> [-f <path>] [--build]`). It loads the config,
// optionally runs pre-start builds, starts the supervisor + control socket,
// and blocks until the supervisor exits or is signalled.
//
// foreground mirrors service output to stdout with colored prefixes; the
// daemon child sets foreground=false so its stdio (the supervisor log file)
// only captures the supervisor's own diagnostics, not duplicated service logs.
func runSupervisor(configPath, projectName string, foreground, runBuilds bool) error {
	cfg, err := loadConfig(configPath, projectName)
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

	if runBuilds {
		if err := runAllBuilds(cfg); err != nil {
			return err
		}
	}

	sup, err := supervisor.New(supervisor.Options{
		Locations:            locs,
		File:                 cfg.File,
		Order:                cfg.Order,
		BaseDir:              cfg.BaseDir,
		Foreground:           foreground,
		Stdout:               os.Stdout,
		InstallSignalHandler: true,
	})
	if err != nil {
		return fmt.Errorf("supervisor: %w", err)
	}

	srv := control.NewServer(locs.Socket, supervisor.NewControlBackend(sup))
	if err := srv.ListenAndServe(); err != nil {
		_ = sup.Close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sup.Start(ctx); err != nil {
		_ = srv.Close()
		_ = sup.Close()
		return fmt.Errorf("supervisor start: %w", err)
	}

	sup.Wait()
	_ = srv.Close()
	_ = sup.Close()

	// The daemon child owns the pidfile; clean it up on exit so `down` /
	// subsequent `up -d` don't see a stale entry. Foreground mode never
	// writes one.
	if !foreground {
		_ = daemon.RemovePidfile(locs)
	}
	return nil
}

// waitForSupervisorExit polls the pidfile until the supervisor process is gone
// or the timeout elapses. Used by `down` to confirm the daemon actually exited
// after the Stop request returns.
func waitForSupervisorExit(locs *project.Locations, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid, err := daemon.ReadPidfile(locs.Pidfile)
		if err != nil {
			return
		}
		if !daemon.IsAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
