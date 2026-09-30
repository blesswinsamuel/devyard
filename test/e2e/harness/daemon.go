package harness

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

// NewUnixClient returns a Connect client speaking h2c over the unix socket
// at sock, and a func that closes its connections.
func NewUnixClient(sock string) (devyardv1connect.DaemonServiceClient, func()) {
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
		ReadIdleTimeout: 15 * time.Second,
		PingTimeout:     5 * time.Second,
	}
	hc := &http.Client{Transport: tr}
	return devyardv1connect.NewDaemonServiceClient(hc, "http://localhost"), tr.CloseIdleConnections
}

// Client returns the sandbox's shared control client. It never starts the
// daemon; calls fail with Unavailable while none runs.
func (sb *Sandbox) Client() devyardv1connect.DaemonServiceClient {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.client == nil {
		sb.client, sb.closeCli = NewUnixClient(sb.SocketPath())
	}
	return sb.client
}

// NewClient returns a dedicated client with its own connection; close() drops
// it (e.g. to simulate a client going away mid-operation).
func (sb *Sandbox) NewClient() (devyardv1connect.DaemonServiceClient, func()) {
	return NewUnixClient(sb.SocketPath())
}

// DaemonInfo returns the daemon's info, or nil when it doesn't answer.
func (sb *Sandbox) DaemonInfo() *v1.DaemonInfo {
	ctx, cancel := context.WithTimeout(sb.ctx, 2*time.Second)
	defer cancel()
	resp, err := sb.Client().GetDaemon(ctx, connect.NewRequest(&v1.GetDaemonRequest{}))
	if err != nil {
		return nil
	}
	return resp.Msg.GetInfo()
}

// DaemonRunning reports whether a daemon answers on the sandbox socket.
func (sb *Sandbox) DaemonRunning() bool { return sb.DaemonInfo() != nil }

// PidfilePid reads the daemon pidfile (0 when absent or invalid).
func (sb *Sandbox) PidfilePid() int {
	data, err := os.ReadFile(sb.PidfilePath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// Daemon is a handle on the sandbox daemon.
type Daemon struct {
	sb *Sandbox
}

// Daemon returns the daemon handle, starting the daemon with
// `devyard daemon start` if none answers, and waiting until it's ready.
func (sb *Sandbox) Daemon() *Daemon {
	sb.t.Helper()
	d := &Daemon{sb: sb}
	if !sb.DaemonRunning() {
		sb.CLI("daemon", "start").MustSucceed(sb.t)
	}
	d.WaitReady()
	sb.ensureRecorder()
	return d
}

// WaitReady waits until the daemon answers GetDaemon and isn't draining,
// and verifies it is this sandbox's daemon.
func (d *Daemon) WaitReady() {
	sb := d.sb
	sb.t.Helper()
	var info *v1.DaemonInfo
	Eventually(sb.t, "daemon ready", func(c *C) {
		info = sb.DaemonInfo()
		if info == nil {
			c.Fatalf("daemon not answering on %s", sb.SocketPath())
		}
		if info.GetDraining() {
			c.Errorf("daemon pid %d is draining", info.GetPid())
		}
	}, WithDiagnostics(sb.daemonLogDiag))
	d.verifyOurs(int(info.GetPid()))
	sb.mu.Lock()
	sb.lastPid = int(info.GetPid())
	sb.mu.Unlock()
}

// verifyOurs is a second guard: the daemon process answering our socket
// must carry our sandbox tag.
func (d *Daemon) verifyOurs(pid int) {
	sb := d.sb
	procs, err := ListProcesses()
	if err != nil {
		sb.t.Logf("cannot verify daemon tag: %v", err)
		return
	}
	for _, p := range procs {
		if p.Pid != pid {
			continue
		}
		if !p.EnvReadable {
			sb.t.Logf("cannot read daemon pid %d environment; skipping tag check", pid)
			return
		}
		if tag := p.SandboxID(); tag != sb.ID {
			sb.t.Fatalf("SANDBOX GUARD: daemon pid %d on %s has sandbox tag %q, want %q — refusing to drive a foreign daemon", pid, sb.SocketPath(), tag, sb.ID)
		}
		return
	}
}

// Sandbox returns the owning sandbox.
func (d *Daemon) Sandbox() *Sandbox { return d.sb }

// Client returns the control client.
func (d *Daemon) Client() devyardv1connect.DaemonServiceClient { return d.sb.Client() }

// Ctx returns a context bounded by DefaultWait (scaled) and the sandbox.
func (d *Daemon) Ctx() context.Context {
	ctx, cancel := context.WithTimeout(d.sb.ctx, Scale(DefaultWait*3))
	d.sb.t.Cleanup(cancel)
	return ctx
}

// Info returns fresh daemon info, failing the test when unreachable.
func (d *Daemon) Info() *v1.DaemonInfo {
	d.sb.t.Helper()
	info := d.sb.DaemonInfo()
	if info == nil {
		d.sb.t.Fatalf("daemon not reachable on %s\n%s", d.sb.SocketPath(), d.sb.daemonLogDiag())
	}
	return info
}

// Pid returns the daemon's pid.
func (d *Daemon) Pid() int { d.sb.t.Helper(); return int(d.Info().GetPid()) }

// WebURL returns http://<web_addr> (re-queried: ports change on restart).
func (d *Daemon) WebURL() string {
	d.sb.t.Helper()
	addr := d.Info().GetWebAddr()
	if addr == "" {
		d.sb.t.Fatalf("daemon reports no web_addr")
	}
	return "http://" + addr
}

// ProxyURL returns http://<proxy_addr>.
func (d *Daemon) ProxyURL() string {
	d.sb.t.Helper()
	addr := d.Info().GetProxyAddr()
	if addr == "" {
		d.sb.t.Fatalf("daemon reports no proxy_addr")
	}
	return "http://" + addr
}

// State fetches GetState.
func (d *Daemon) State() State {
	d.sb.t.Helper()
	ctx, cancel := context.WithTimeout(d.sb.ctx, Scale(10*time.Second))
	defer cancel()
	resp, err := d.Client().GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
	if err != nil {
		d.sb.t.Fatalf("GetState: %v\n%s", err, d.sb.daemonLogDiag())
	}
	s := StateFromSnapshot(resp.Msg.GetRevision(), resp.Msg.GetSnapshot())
	d.sb.recordState(s)
	return s
}

// KillHard SIGKILLs the daemon process only (its runners and services
// must survive) and waits for it to disappear.
func (d *Daemon) KillHard() {
	sb := d.sb
	sb.t.Helper()
	pid := d.Pid()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		sb.t.Fatalf("kill daemon %d: %v", pid, err)
	}
	Eventually(sb.t, "daemon process gone", func(c *C) {
		if PidAlive(pid) {
			c.Errorf("pid %d still alive", pid)
		}
	})
}

// Restart restarts the daemon over RPC and waits for the replacement.
// Identity is (pid, started_at) because the replacement may exec in place.
func (d *Daemon) Restart(restartServices bool) {
	sb := d.sb
	sb.t.Helper()
	old := d.Info()
	ctx, cancel := context.WithTimeout(sb.ctx, Scale(60*time.Second))
	defer cancel()
	_, err := d.Client().RestartDaemon(ctx, connect.NewRequest(&v1.RestartDaemonRequest{RestartServices: restartServices}))
	if err != nil && connect.CodeOf(err) != connect.CodeUnavailable && connect.CodeOf(err) != connect.CodeUnknown {
		sb.t.Fatalf("RestartDaemon: %v", err)
	}
	d.WaitReplaced(old)
}

// WaitReplaced waits until a daemon other than old is ready.
func (d *Daemon) WaitReplaced(old *v1.DaemonInfo) {
	sb := d.sb
	sb.t.Helper()
	Eventually(sb.t, "replacement daemon ready", func(c *C) {
		info := sb.DaemonInfo()
		if info == nil {
			c.Fatalf("no daemon answering")
		}
		if info.GetPid() == old.GetPid() && info.GetStartedAtUnixMs() == old.GetStartedAtUnixMs() {
			c.Fatalf("still the old daemon (pid %d)", info.GetPid())
		}
		if info.GetDraining() {
			c.Errorf("new daemon draining")
		}
	}, Within(60*time.Second), WithDiagnostics(sb.daemonLogDiag))
	d.WaitReady()
}

// Stop stops the daemon with `devyard daemon stop` and waits for its
// process to exit.
func (d *Daemon) Stop() {
	sb := d.sb
	sb.t.Helper()
	pid := d.Pid()
	sb.CLI("daemon", "stop").MustSucceed(sb.t)
	Eventually(sb.t, "daemon exited", func(c *C) {
		if PidAlive(pid) {
			c.Errorf("daemon pid %d still alive", pid)
		}
	}, Within(30*time.Second))
}
