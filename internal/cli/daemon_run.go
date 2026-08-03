package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/orchestrator"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// runDaemonChild is the entry point for the daemonized global daemon. It
// creates the orchestrator, starts the control server on the daemon socket,
// optionally starts the web UI (if enabled in global config), installs a
// signal handler, and blocks until StopDaemon is called or a signal is
// received.
func runDaemonChild() error {
	locs, err := project.ResolveDaemon()
	if err != nil {
		return fmt.Errorf("resolve daemon paths: %w", err)
	}
	if err := locs.MkdirAll(); err != nil {
		return err
	}

	d := orchestrator.New()

	srv := control.NewServer(locs.Socket, d)
	if err := srv.ListenAndServe(); err != nil {
		return err
	}

	// Autostart projects with restart: always or restart: unless-stopped
	// (unless a .stopped marker exists).
	started, skipped, err := d.Autostart()
	if err != nil {
		fmt.Fprintf(os.Stderr, "local-compose: autostart: %v\n", err)
	}
	if started > 0 || skipped > 0 {
		fmt.Fprintf(os.Stderr, "local-compose: autostart: %d started, %d skipped\n", started, skipped)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-d.StopCh():
	case <-sigCh:
		_ = d.StopDaemon()
	}

	_ = srv.Close()
	_ = daemon.RemoveDaemonPidfile(locs)
	return nil
}
