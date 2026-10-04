//go:build unix

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/blesswinsamuel/devyard/internal/api"
	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/events"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
	"github.com/blesswinsamuel/devyard/internal/gitstate"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/procstat"
	"github.com/blesswinsamuel/devyard/internal/proxy"
	"github.com/blesswinsamuel/devyard/internal/runner"
	"github.com/blesswinsamuel/devyard/internal/sessions"
	"github.com/blesswinsamuel/devyard/internal/web"
)

// Options configure Run.
type Options struct {
	Version string
	// LockWait is how long to wait for a previous daemon to release the
	// lock (during a restart handover).
	LockWait time.Duration
}

type exitMode int

const (
	exitDetach  exitMode = iota // leave services running (signal)
	exitStop                    // stop services, then exit
	exitRestart                 // hand over to a new daemon
)

type daemon struct {
	opts    Options
	dirs    paths.Dirs
	log     *slog.Logger
	started time.Time

	mgr *engine.Manager
	bus *events.Bus

	gcfgMu sync.Mutex
	gcfg   *globalconfig.Config

	webAddr, proxyAddr, proxyTLSAddr string

	exitOnce        sync.Once
	exitCh          chan exitMode
	restartServices bool
}

// Run runs the daemon until it is asked to stop.
func Run(opts Options) (err error) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	defer func() {
		if err != nil {
			log.Error("daemon failed", "error", err)
		}
	}()
	if opts.LockWait == 0 {
		// A handover releases the lock before spawning the replacement,
		// so waiting long only keeps losers of a startup race around.
		opts.LockWait = 2 * time.Second
	}
	dirs, err := paths.Default()
	if err != nil {
		return err
	}
	lockFile, err := lock(dirs, opts.LockWait)
	if err != nil {
		return err
	}
	lockReleased := false
	releaseLock := func() {
		if !lockReleased {
			lockReleased = true
			removePidIfOurs(dirs)
			_ = lockFile.Close()
		}
	}
	defer releaseLock()
	if err := writePid(dirs); err != nil {
		return fmt.Errorf("daemon: write pidfile: %w", err)
	}

	gcfg, err := globalconfig.Load(dirs.GlobalConfig(), os.Stderr)
	if err != nil {
		return err
	}
	d := &daemon{
		opts:    opts,
		dirs:    dirs,
		log:     log,
		started: time.Now(),
		bus:     events.New(),
		gcfg:    gcfg,
		exitCh:  make(chan exitMode, 1),
	}
	log.Info("daemon starting", "pid", os.Getpid(), "version", opts.Version, "config", gcfg.String())

	// Fail closed: a malformed hash refuses to start rather than serving
	// an open dashboard.
	var auth *web.Authenticator
	if gcfg.Web.PasswordHash != "" {
		if auth, err = web.NewAuthenticator(gcfg.Web.PasswordHash); err != nil {
			return fmt.Errorf("daemon: %w", err)
		}
	}

	// Bind every listener before touching projects so a port collision is
	// reported immediately and URLs are known when services are published.
	_ = os.Remove(dirs.Socket())
	ctlLn, err := net.Listen("unix", dirs.Socket())
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", dirs.Socket(), err)
	}
	_ = os.Chmod(dirs.Socket(), 0o600)
	webLn, err := net.Listen("tcp", net.JoinHostPort(gcfg.Web.Host, strconv.Itoa(gcfg.Web.Port)))
	if err != nil {
		_ = ctlLn.Close()
		return fmt.Errorf("daemon: web dashboard: listen %s:%d: %w", gcfg.Web.Host, gcfg.Web.Port, err)
	}
	d.webAddr = webLn.Addr().String()

	git, err := gitstate.New(d.bus, log)
	if err != nil {
		_ = ctlLn.Close()
		_ = webLn.Close()
		return err
	}
	defer git.Close()

	var mgr *engine.Manager
	resolver := &lateRoutes{}
	psrv, err := d.startProxy(resolver)
	if err != nil {
		_ = ctlLn.Close()
		_ = webLn.Close()
		return err
	}
	d.proxyAddr = psrv.Addr()
	if gcfg.Proxy.TLS.Enabled {
		d.proxyTLSAddr = psrv.TLSAddr()
	}

	presenter := newPresenter(d.bus, git, d.urlConfig())
	mgr = engine.NewManager(dirs, engine.RunnerLauncher{}, presenter, log)
	d.mgr = mgr
	resolver.set(routes{mgr: mgr, bus: d.bus})
	d.bus.SetDaemon(d.staticInfo())
	if err := mgr.Load(); err != nil {
		log.Error("loading projects failed", "error", err)
	}
	sess := sessions.New(dirs, mgr, runner.LaunchOptions{})

	apiServer := &api.Server{Mgr: mgr, Bus: d.bus, Git: git, Sessions: sess, Daemon: d, Log: log}
	path, apiHandler := devyardv1connect.NewDaemonServiceHandler(apiServer)
	apiMux := http.NewServeMux()
	apiMux.Handle(path, apiHandler)

	// The control socket speaks unencrypted HTTP/2 so the CLI can use
	// bidirectional streams (Attach).
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	ctlSrv := &http.Server{Handler: apiMux, Protocols: protocols}
	go func() { _ = ctlSrv.Serve(ctlLn) }()

	dashboard := web.Handler(web.Options{API: apiMux, Sessions: sess, Hosts: d.hostPolicy, Auth: auth, Log: log})
	webSrv := &http.Server{Handler: dashboard, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = webSrv.Serve(webLn) }()
	resolver.setDashboard(dashboard)

	log.Info("daemon ready", "socket", dirs.Socket(), "web", d.webAddr, "proxy", d.proxyAddr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	var mode exitMode
	select {
	case mode = <-d.exitCh:
	case sig := <-sigCh:
		log.Info("signal received; exiting and leaving services running", "signal", sig.String())
		mode = exitDetach
	}

	// Let the RPC that requested the exit finish its response.
	time.Sleep(100 * time.Millisecond)
	stopServices := mode == exitStop || (mode == exitRestart && d.restartServices)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := mgr.Shutdown(shutdownCtx, stopServices); err != nil {
		log.Error("shutdown", "error", err)
	}
	if mode == exitStop {
		sess.StopAll(shutdownCtx)
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	_ = ctlSrv.Shutdown(closeCtx)
	_ = webSrv.Shutdown(closeCtx)
	cancelClose()
	_ = ctlSrv.Close()
	_ = webSrv.Close()
	_ = psrv.Close()
	_ = os.Remove(dirs.Socket())
	releaseLock()

	if mode == exitRestart {
		pid, err := Spawn(dirs, os.Environ())
		if err != nil {
			return fmt.Errorf("daemon: spawn replacement: %w", err)
		}
		log.Info("handed over to replacement daemon", "pid", pid)
	}
	log.Info("daemon exited")
	return nil
}

// lateRoutes lets the proxy start before the manager exists.
type lateRoutes struct {
	mu        sync.RWMutex
	r         proxy.Resolver
	dashboard http.Handler
}

func (l *lateRoutes) set(r proxy.Resolver) {
	l.mu.Lock()
	l.r = r
	l.mu.Unlock()
}

func (l *lateRoutes) setDashboard(h http.Handler) {
	l.mu.Lock()
	l.dashboard = h
	l.mu.Unlock()
}

func (l *lateRoutes) ProxyRoutes() []proxy.Route {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.r == nil {
		return nil
	}
	return l.r.ProxyRoutes()
}

func (l *lateRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.RLock()
	h := l.dashboard
	l.mu.RUnlock()
	if h == nil {
		http.Error(w, "devyard: starting", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

func (d *daemon) startProxy(resolver *lateRoutes) (*proxy.Server, error) {
	cfg := d.gcfg.Proxy
	opts := proxy.ServerOptions{
		Addr:             net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		DomainSuffix:     cfg.DomainSuffix,
		Resolver:         resolver,
		DashboardHandler: resolver,
	}
	if cfg.TLS.Enabled {
		cm, err := proxy.NewCertManager(proxy.CertManagerOptions{
			CertFile: cfg.TLS.CertFile,
			KeyFile:  cfg.TLS.KeyFile,
			CADir:    filepath.Join(d.dirs.State, "ca"),
		})
		if err != nil {
			return nil, fmt.Errorf("daemon: proxy tls: %w", err)
		}
		opts.TLS = proxy.TLSOptions{
			Enabled:      true,
			Addr:         net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.EffectiveTLSPort())),
			TLSConfig:    cm.TLSConfig(),
			HTTPRedirect: cfg.TLS.HTTPRedirect,
		}
	}
	srv := proxy.NewServer(opts)
	if err := srv.ListenAndServe(); err != nil {
		return nil, fmt.Errorf("daemon: reverse proxy: %w", err)
	}
	return srv, nil
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func (d *daemon) urlConfig() urlConfig {
	u := urlConfig{Suffix: d.gcfg.Proxy.DomainSuffix, Port: portOf(d.proxyAddr)}
	if d.gcfg.Proxy.TLS.Enabled {
		u.TLS = true
		u.Port = portOf(d.proxyTLSAddr)
	}
	return u
}

func (d *daemon) hostPolicy() web.HostPolicy {
	d.gcfgMu.Lock()
	defer d.gcfgMu.Unlock()
	return web.HostPolicy{DomainSuffix: d.gcfg.Proxy.DomainSuffix, Extra: d.gcfg.Web.AllowedHosts}
}

func (d *daemon) staticInfo() *pb.DaemonInfo {
	return &pb.DaemonInfo{
		Pid:             int32(os.Getpid()),
		StartedAtUnixMs: d.started.UnixMilli(),
		Version:         d.opts.Version,
		GoVersion:       runtime.Version(),
		WebAddr:         d.webAddr,
		ProxyAddr:       d.proxyAddr,
		ProxyTlsAddr:    d.proxyTLSAddr,
		DomainSuffix:    d.gcfg.Proxy.DomainSuffix,
		Draining:        d.mgr != nil && d.mgr.Draining(),
	}
}

// --- api.Daemon ----------------------------------------------------------------

// Info implements api.Daemon.
func (d *daemon) Info() *pb.DaemonInfo {
	info := d.staticInfo()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	info.Goroutines = int32(runtime.NumGoroutine())
	info.MemoryHeap = ms.HeapAlloc
	if s, ok := procstat.SampleGroup(syscall.Getpgrp()); ok {
		info.MemoryRss = s.RSS
	}
	return info
}

func (d *daemon) requestExit(mode exitMode) {
	d.exitOnce.Do(func() {
		d.bus.SetDaemon(func() *pb.DaemonInfo { i := d.staticInfo(); i.Draining = true; return i }())
		d.exitCh <- mode
	})
}

// RequestStop implements api.Daemon.
func (d *daemon) RequestStop() { d.requestExit(exitStop) }

// RequestRestart implements api.Daemon.
func (d *daemon) RequestRestart(restartServices bool) {
	d.restartServices = restartServices
	d.requestExit(exitRestart)
}

// GlobalConfig implements api.Daemon.
func (d *daemon) GlobalConfig() (*globalconfig.Config, string, error) {
	cfg, err := globalconfig.Load(d.dirs.GlobalConfig(), nil)
	return cfg, d.dirs.GlobalConfig(), err
}

// SaveGlobalConfig implements api.Daemon. Listener changes apply after a
// daemon restart; allowed hosts apply immediately. The password hash is
// managed by `devyard auth` and is not part of the wire schema, so
// whatever is on disk is preserved.
func (d *daemon) SaveGlobalConfig(cfg *globalconfig.Config) error {
	cur, err := globalconfig.Load(d.dirs.GlobalConfig(), nil)
	if err != nil {
		return fmt.Errorf("daemon: read global config: %w", err)
	}
	cfg.Web.PasswordHash = cur.Web.PasswordHash
	if err := globalconfig.Save(d.dirs.GlobalConfig(), cfg); err != nil {
		return err
	}
	d.gcfgMu.Lock()
	d.gcfg.Web.AllowedHosts = cfg.Web.AllowedHosts
	d.gcfgMu.Unlock()
	return nil
}

var _ api.Daemon = (*daemon)(nil)

// ErrNotRunning is returned by clients when no daemon answers.
var ErrNotRunning = errors.New("no daemon running")
