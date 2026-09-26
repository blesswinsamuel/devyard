package cli

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/daemon"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/orchestrator"
	"github.com/blesswinsamuel/devyard/internal/project"
	"github.com/blesswinsamuel/devyard/internal/proxy"
)

// runDaemonChild is the entry point for the daemonized global daemon. It
// creates the orchestrator, starts the control server on the daemon socket,
// installs a signal handler, and blocks until StopDaemon is called or a
// signal is received. The web UI is a separate process (`devyard web`).
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
		return fmt.Errorf("devyard daemon: %w", err)
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

	// Reverse proxy exposing services that declare port(s) at named URLs
	// (<service>.<project>.<suffix>). Settings come from the global config;
	// loopback-only by default. A failed listen (e.g. port in use) disables
	// the proxy but does not take the daemon down.
	gcfg, err := globalconfig.Load()
	if err != nil {
		slog.Warn("global config unavailable, reverse proxy disabled", "error", err)
	} else {
		psrv := proxy.NewServer(
			fmt.Sprintf("%s:%d", gcfg.Proxy.Host, gcfg.Proxy.Port),
			gcfg.Proxy.DomainSuffix,
			d,
		)
		if err := psrv.ListenAndServe(); err != nil {
			slog.Error("reverse proxy disabled; listen failed", "addr", psrv.Addr(), "error", err)
		} else {
			defer func() { _ = psrv.Close() }()
			port := gcfg.Proxy.Port
			if _, portStr, err := net.SplitHostPort(psrv.Addr()); err == nil {
				if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
					port = p
				}
			}
			d.SetProxyInfo(port, gcfg.Proxy.DomainSuffix)
		}
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
