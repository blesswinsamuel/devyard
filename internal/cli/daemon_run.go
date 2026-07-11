package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/globalconfig"
	"github.com/blesswinsamuel/local-compose/internal/orchestrator"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/web"
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

	cfg, err := globalconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "local-compose: global config: %v\n", err)
		return err
	}
	fmt.Fprintf(os.Stderr, "local-compose: global config: %s\n", cfg)

	d := orchestrator.New()

	srv := control.NewServer(locs.Socket, d)
	if err := srv.ListenAndServe(); err != nil {
		return err
	}

	// Start the web UI if enabled in global config.
	var webSrv *web.Server
	if cfg.Web.Enabled {
		webAddr := fmt.Sprintf("%s:%d", cfg.Web.Host, cfg.Web.Port)
		webSrv = web.NewServer(webAddr, d)
		if err := webSrv.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "local-compose: web UI: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "local-compose: web UI at http://%s\n", webSrv.Addr())
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-d.StopCh():
	case <-sigCh:
		_ = d.StopDaemon()
	}

	if webSrv != nil {
		_ = webSrv.Close()
	}
	_ = srv.Close()
	_ = daemon.RemoveDaemonPidfile(locs)
	return nil
}
