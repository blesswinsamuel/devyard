package cli

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/daemon"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/orchestrator"
	"github.com/blesswinsamuel/devyard/internal/project"
	"github.com/blesswinsamuel/devyard/internal/proxy"
	"github.com/blesswinsamuel/devyard/internal/web"
)

// runDaemonChild is the entry point for the daemonized global daemon. It
// creates the orchestrator, starts the control server on the daemon socket,
// starts the web dashboard server and reverse proxy, installs a signal handler,
// and blocks until StopDaemon is called or a signal is received.
func runDaemonChild() (err error) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	defer func() {
		if err != nil {
			slog.Error("daemon startup failed", "error", err)
		}
	}()

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
	defer func() { _ = srv.Close() }()

	gcfg, err := globalconfig.Load()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	// Web dashboard server
	_, rpcHandler := srv.Handler()
	wsrv := web.NewServer(fmt.Sprintf("%s:%d", gcfg.Web.Host, gcfg.Web.Port), rpcHandler, d)
	if err := wsrv.ListenAndServe(); err != nil {
		return fmt.Errorf("web server: %w", err)
	}
	defer func() { _ = wsrv.Close() }()

	serverOpts := proxy.ServerOptions{
		Addr:             fmt.Sprintf("%s:%d", gcfg.Proxy.Host, gcfg.Proxy.Port),
		DomainSuffix:     gcfg.Proxy.DomainSuffix,
		Resolver:         d,
		DashboardHandler: wsrv.Handler(),
	}

	if gcfg.Proxy.TLS.Enabled {
		tlsPort := gcfg.Proxy.EffectiveTLSPort()
		tlsAddr := fmt.Sprintf("%s:%d", gcfg.Proxy.Host, tlsPort)
		cm, err := proxy.NewCertManager(proxy.CertManagerOptions{
			CertFile: gcfg.Proxy.TLS.CertFile,
			KeyFile:  gcfg.Proxy.TLS.KeyFile,
			CADir:    filepath.Join(locs.State, "ca"),
		})
		if err != nil {
			return fmt.Errorf("proxy tls cert manager: %w", err)
		}
		serverOpts.TLS = proxy.TLSOptions{
			Enabled:      true,
			Addr:         tlsAddr,
			TLSConfig:    cm.TLSConfig(),
			HTTPRedirect: gcfg.Proxy.TLS.HTTPRedirect,
		}
	}

	psrv := proxy.NewServer(serverOpts)
	if err := psrv.ListenAndServe(); err != nil {
		return fmt.Errorf("reverse proxy: %w", err)
	}
	defer func() { _ = psrv.Close() }()

	port := gcfg.Proxy.Port
	if _, portStr, err := net.SplitHostPort(psrv.Addr()); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}
	tlsPort := 0
	if gcfg.Proxy.TLS.Enabled {
		tlsPort = gcfg.Proxy.EffectiveTLSPort()
		if _, portStr, err := net.SplitHostPort(psrv.TLSAddr()); err == nil {
			if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
				tlsPort = p
			}
		}
	}
	d.SetProxyInfo(port, tlsPort, gcfg.Proxy.DomainSuffix, gcfg.Proxy.TLS.Enabled)

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

	_ = daemon.RemoveDaemonPidfile(locs)
	slog.Info("daemon exited")
	return nil
}
