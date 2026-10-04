//go:build unix

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/web"
)

// configDebounce lets an editor finish writing (and renaming) before the
// file is read.
const configDebounce = 150 * time.Millisecond

// oldListenerGrace is how long a replaced web listener keeps serving
// requests that are already in flight (including the one that changed it).
const oldListenerGrace = 3 * time.Second

// serveWeb serves the dashboard on ln.
func (d *daemon) serveWeb(ln net.Listener) *http.Server {
	srv := &http.Server{Handler: d.dashboard, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return srv
}

// watchGlobalConfig applies the global config whenever its file changes,
// until ctx ends. The directory is watched, not the file, so editors that
// replace the file (and our own atomic writes) are seen.
func (d *daemon) watchGlobalConfig(ctx context.Context) error {
	path := d.dirs.GlobalConfig()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return err
	}
	go func() {
		defer func() { _ = w.Close() }()
		fire := make(chan struct{}, 1)
		var timer *time.Timer
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Clean(ev.Name) != path {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(configDebounce, func() {
					select {
					case fire <- struct{}{}:
					default:
					}
				})
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				d.log.Warn("watching the global config", "error", err)
			case <-fire:
				_ = d.reloadGlobalConfig(ctx)
			}
		}
	}()
	return nil
}

// reloadGlobalConfig reads the config file and applies it. A file that does
// not parse changes nothing (the last good config stays in effect) and is
// reported through DaemonInfo.config_error, as is any part that could not be
// applied.
func (d *daemon) reloadGlobalConfig(ctx context.Context) error {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	cfg, err := globalconfig.Load(d.dirs.GlobalConfig(), logWriter{d})
	if err != nil {
		d.setConfigError(err)
		return err
	}
	err = d.applyGlobalConfig(ctx, cfg)
	d.setConfigError(err)
	return err
}

// logWriter sends config warnings (unknown fields) to the daemon log.
type logWriter struct{ d *daemon }

func (w logWriter) Write(p []byte) (int, error) {
	w.d.log.Warn("global config", "warning", strings.TrimSpace(string(p)))
	return len(p), nil
}

// setConfigError records (and publishes) what could not be applied; nil
// clears it.
func (d *daemon) setConfigError(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
		d.log.Warn("global config not fully applied", "error", err)
	}
	d.gcfgMu.Lock()
	changed := d.configErr != msg
	d.configErr = msg
	d.gcfgMu.Unlock()
	if changed && d.bus != nil {
		d.bus.SetDaemon(d.staticInfo())
	}
}

// applyGlobalConfig brings the daemon in line with next: the password gate,
// allowed hosts, the proxy and web listeners, and the project list. Each part
// is applied independently; a part that fails keeps its old behavior and is
// reported in the returned error.
func (d *daemon) applyGlobalConfig(ctx context.Context, next *globalconfig.Config) error {
	cur := d.config()
	var errs []error

	if next.Web.PasswordHash != cur.Web.PasswordHash {
		if err := d.setAuth(next.Web.PasswordHash); err != nil {
			errs = append(errs, err)
		}
	}
	d.gcfgMu.Lock()
	d.gcfg.Web.AllowedHosts = next.Web.AllowedHosts
	d.gcfg.Projects, d.gcfg.ProjectsSet, d.gcfg.Groups = next.Projects, next.ProjectsSet, next.Groups
	d.gcfgMu.Unlock()

	if !reflect.DeepEqual(next.Proxy, cur.Proxy) {
		if err := d.rebindProxy(ctx, next.Proxy); err != nil {
			errs = append(errs, err)
		}
	}
	if next.Web.Host != cur.Web.Host || next.Web.Port != cur.Web.Port {
		if err := d.rebindWeb(next.Web.Host, next.Web.Port); err != nil {
			errs = append(errs, err)
		}
	}
	if err := d.projects.Reconcile(ctx, next); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// setAuth swaps the live password gate; an empty hash opens the dashboard.
func (d *daemon) setAuth(hash string) error {
	var auth *web.Authenticator
	if hash != "" {
		var err error
		if auth, err = web.NewAuthenticator(hash); err != nil {
			return fmt.Errorf("web.password_hash: %w", err)
		}
	}
	d.gcfgMu.Lock()
	d.gcfg.Web.PasswordHash = hash
	d.auth = auth
	d.gcfgMu.Unlock()
	d.log.Info("dashboard password changed", "set", hash != "")
	return nil
}

// sharesSocket reports whether the new proxy config wants a socket the old
// one holds, so the old one must let go before the new one can bind.
func sharesSocket(a, b globalconfig.ProxyConfig) bool {
	if a.Host != b.Host {
		return false
	}
	if a.Port != 0 && a.Port == b.Port {
		return true
	}
	return a.TLS.Enabled && b.TLS.Enabled && a.EffectiveTLSPort() == b.EffectiveTLSPort()
}

// rebindProxy replaces the reverse proxy with one configured by next. The
// new listeners are bound before the old ones are closed, so a bad address
// leaves the old proxy running; when both want the same port the old one is
// closed first and restored if the new one still cannot bind.
func (d *daemon) rebindProxy(ctx context.Context, next globalconfig.ProxyConfig) error {
	d.netMu.Lock()
	cur := d.config().Proxy
	old := d.proxySrv
	srv, err := d.startProxy(d.resolver, next)
	if err != nil && sharesSocket(cur, next) {
		_ = old.Close()
		if srv, err = d.startProxy(d.resolver, next); err != nil {
			if restored, rerr := d.startProxy(d.resolver, cur); rerr == nil {
				d.proxySrv = restored
			} else {
				d.log.Error("could not restore the reverse proxy", "error", rerr)
			}
			d.netMu.Unlock()
			return fmt.Errorf("proxy: %w", err)
		}
	} else if err != nil {
		d.netMu.Unlock()
		return fmt.Errorf("proxy: %w", err)
	} else {
		_ = old.Close()
	}
	d.proxySrv = srv
	d.proxyAddr, d.proxyTLSAddr = srv.Addr(), ""
	if next.TLS.Enabled {
		d.proxyTLSAddr = srv.TLSAddr()
	}
	d.gcfgMu.Lock()
	d.gcfg.Proxy = next
	d.gcfgMu.Unlock()
	d.presenter.setURLs(d.urlConfig())
	d.netMu.Unlock()
	d.bus.SetDaemon(d.staticInfo())
	d.mgr.Republish(ctx)
	d.log.Info("reverse proxy rebound", "addr", srv.Addr(), "suffix", next.DomainSuffix)
	return nil
}

// rebindWeb moves the dashboard to host:port. The new listener is bound
// first, so a bad address leaves the old one serving; the old one is closed
// after a short grace period.
func (d *daemon) rebindWeb(host string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("web dashboard: %w", err)
	}
	d.netMu.Lock()
	old := d.webSrv
	d.webSrv = d.serveWeb(ln)
	d.webAddr = ln.Addr().String()
	d.netMu.Unlock()
	d.gcfgMu.Lock()
	d.gcfg.Web.Host, d.gcfg.Web.Port = host, port
	d.gcfgMu.Unlock()
	d.bus.SetDaemon(d.staticInfo())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), oldListenerGrace)
		defer cancel()
		_ = old.Shutdown(ctx)
		_ = old.Close()
	}()
	d.log.Info("web dashboard rebound", "addr", ln.Addr().String())
	return nil
}

// errNotApplied wraps a failure to apply a config that was saved.
func errNotApplied(err error) error {
	return fmt.Errorf("%w: saved, but not applied: %v", engine.ErrConfig, err)
}
