package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/orchestrator"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// runDaemonChild is the entry point for the daemonized global daemon. It
// creates the orchestrator, starts the control server on the daemon socket,
// installs a signal handler, and blocks until StopDaemon is called or a
// signal is received. The web UI is a separate process (`local-compose web`).
func runDaemonChild() error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	locs, err := project.ResolveDaemon()
	if err != nil {
		return fmt.Errorf("resolve daemon paths: %w", err)
	}
	if err := locs.MkdirAll(); err != nil {
		return err
	}

	lockFile, err := daemon.LockDaemon(locs)
	if err != nil {
		return fmt.Errorf("local-compose daemon: %w", err)
	}
	pid := os.Getpid()
	defer func() {
		_ = lockFile.Close()
		// Remove our pidfile only while it still names this process — a
		// replacement daemon spawned during a restart may have already
		// written its own pid by the time we exit.
		_ = daemon.RemovePidfileIfOurs(locs, pid)
	}()

	if err := daemon.WritePidfile(locs.Pidfile, pid); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}

	slog.Info("daemon starting", "pid", pid, "socket", locs.Socket)

	d := orchestrator.New()

	srv := control.NewServer(locs.Socket, d)
	if err := srv.ListenAndServe(); err != nil {
		return err
	}

	// Autostart all registered projects unless a project-level .stopped
	// marker exists (explicit down/stop).
	started, skipped, err := d.Autostart()
	if err != nil {
		slog.Error("autostart failed", "error", err)
	}
	if started > 0 || skipped > 0 {
		slog.Info("autostart finished", "started", started, "skipped", skipped)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-d.StopCh():
		slog.Info("stop requested")
		time.Sleep(100 * time.Millisecond)
	case sig := <-sigCh:
		slog.Info("signal received, shutting down", "signal", sig.String())
		_ = d.StopDaemon()
	}

	_ = srv.Close()
	_ = daemon.RemoveDaemonPidfile(locs)
	slog.Info("daemon exited")
	return nil
}
