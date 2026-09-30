// Package sessions_test covers interactive sessions (project terminals,
// attached tty services) over the dashboard websocket and the Attach RPC:
// they outlive connections, replay scrollback, and one noisy session can't
// hurt the others.
package sessions_test

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const cfg = `version: "1"
services:
  idle:
    command: {{fixture "ticker"}} -interval 1s
  console:
    command: sh -c 'while read l; do echo "console-got:$l"; done'
    tty: true
`

func setup(t *testing.T, name string) (*harness.Project, *harness.Daemon) {
	t.Helper()
	sb := harness.New(t)
	p := sb.WriteProject(name, cfg, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	return p, d
}

// shellPid asks the shell for its pid.
func shellPid(t *testing.T, s *harness.Session) int {
	t.Helper()
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	re := regexp.MustCompile(`PID:(\d+):` + nonce)
	s.Send(t, `echo "PID:$$:`+nonce+`"`+"\r")
	pid := 0
	harness.Eventually(t, "shell reports its pid", func(c *harness.C) {
		m := re.FindStringSubmatch(harness.StripANSI(s.Output()))
		if m == nil {
			c.Errorf("no pid yet in %q", tailStr(s.Output(), 300))
			return
		}
		pid, _ = strconv.Atoi(m[1])
	})
	return pid
}

func tailStr(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func terminal(p *harness.Project) harness.WSTarget {
	return harness.WSTarget{Kind: "terminal", Project: p.ID}
}

func TestSessions_TerminalRunsInProjectDir(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "term")
	s := d.OpenWS(terminal(p), 80, 24)
	if s.SessionID == "" || !s.TTY {
		t.Fatalf("ready: id=%q tty=%v", s.SessionID, s.TTY)
	}
	s.Send(t, "echo hi-$((40+2))\r")
	s.Expect(t, "hi-42")
	s.Send(t, "pwd\r")
	s.Expect(t, p.Dir)
}

// O14: a terminal survives its connection; reattaching by session id gets
// the same shell and the scrollback.
func TestLedger_O14_ReconnectKeepsShellAndScrollback(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "recon")
	s := d.OpenWS(terminal(p), 80, 24)
	pid := shellPid(t, s)
	s.Send(t, "export DY_SESS=kept-42; echo marker-$((1000+1))\r")
	s.Expect(t, "marker-1001")
	id := s.SessionID
	s.Drop()

	target := terminal(p)
	target.SessionID = id
	s2 := d.OpenWS(target, 80, 24)
	if s2.SessionID != id {
		t.Errorf("reattach returned session %q, want %q", s2.SessionID, id)
	}
	s2.Expect(t, "marker-1001") // scrollback replay
	s2.Send(t, "echo val=$DY_SESS\r")
	s2.Expect(t, "val=kept-42")
	if got := shellPid(t, s2); got != pid {
		t.Errorf("reattached to a different shell: pid %d, was %d", got, pid)
	}
	if !harness.PidAlive(pid) {
		t.Errorf("shell died")
	}
}

// O14: an output burst in one session (with a slow reader) doesn't kill
// or stall the others; the noisy session itself survives.
func TestLedger_O14_BurstDoesNotKillOtherSessions(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "burst")
	noisy := d.OpenWS(terminal(p), 80, 24)
	quiet := d.OpenWS(terminal(p), 80, 24)
	noisyPid := shellPid(t, noisy)
	noisy.Pause()
	noisy.Send(t, "yes | head -c 5000000; echo burst-done\r")
	for i := 0; i < 5; i++ {
		quiet.Send(t, fmt.Sprintf("echo quiet-%d-$((%d*2))\r", i, i))
		quiet.ExpectWithin(t, 5*time.Second, fmt.Sprintf("quiet-%d-%d", i, i*2))
		time.Sleep(200 * time.Millisecond)
	}
	noisy.Resume()
	if !harness.PidAlive(noisyPid) {
		t.Fatalf("noisy session's shell was killed")
	}
	// Output may have been dropped or coalesced, but the session works.
	noisy.Send(t, "echo still-$((6*7))\r")
	noisy.ExpectWithin(t, 30*time.Second, "still-42")
	quiet.Send(t, "echo after-$((1+1))-burst\r")
	quiet.Expect(t, "after-2-burst")
}

func TestSessions_CloseKillsShell(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "closing")
	s := d.OpenWS(terminal(p), 80, 24)
	pid := shellPid(t, s)
	s.Close(t)
	harness.Eventually(t, "shell gone after close", func(c *harness.C) {
		if harness.PidAlive(pid) {
			c.Errorf("shell %d alive", pid)
		}
	})
}

func TestSessions_ResizeReachesPTY(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "resize")
	s := d.OpenWS(terminal(p), 80, 24)
	s.Send(t, "stty size\r")
	s.Expect(t, "24 80")
	s.Resize(t, 100, 40)
	time.Sleep(100 * time.Millisecond)
	s.Send(t, "stty size\r")
	s.Expect(t, "40 100")
}

func TestSessions_TerminalOverAttachRPC(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "rpcterm")
	s := d.Attach(&v1.AttachTarget{Kind: "terminal", Project: p.ID}, 80, 24)
	if !s.TTY || s.SessionID == "" {
		t.Fatalf("ready: %q %v", s.SessionID, s.TTY)
	}
	pid := shellPid(t, s)
	s.Send(t, "export DY_RPC=yes\r")
	id := s.SessionID
	s.Drop()
	s2 := d.Attach(&v1.AttachTarget{Kind: "terminal", Project: p.ID, SessionId: id}, 80, 24)
	s2.Send(t, "echo rpc=$DY_RPC\r")
	s2.Expect(t, "rpc=yes")
	if got := shellPid(t, s2); got != pid {
		t.Errorf("different shell after RPC reattach: %d vs %d", got, pid)
	}
	// The same session is reachable from the websocket too.
	s3 := d.OpenWS(harness.WSTarget{Kind: "terminal", Project: p.ID, SessionID: id}, 80, 24)
	s3.Send(t, "echo ws=$DY_RPC\r")
	s3.Expect(t, "ws=yes")
}

// O14: reloading the project doesn't kill its terminals.
func TestLedger_O14_ReloadKeepsTerminals(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "reloadterm")
	s := d.OpenWS(terminal(p), 80, 24)
	pid := shellPid(t, s)
	_, err := d.Client().ReloadProject(d.Ctx(), connect.NewRequest(&v1.ReloadProjectRequest{Project: p.ID}))
	harness.NoError(t, err, "ReloadProject")
	s.Send(t, "echo after-$((2+2))-reload\r")
	s.Expect(t, "after-4-reload")
	if !harness.PidAlive(pid) {
		t.Errorf("shell killed by reload")
	}
}

func TestSessions_AttachTTYServiceOverWS(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "svcatt")
	s := d.OpenWS(harness.WSTarget{Kind: "service", Project: p.ID, Name: "console"}, 80, 24)
	if !s.TTY {
		t.Errorf("tty service attached without tty")
	}
	s.Send(t, "ping\r")
	s.Expect(t, "console-got:ping")
	s.Drop()
	// A second client sees the service still alive and interactive.
	s2 := d.OpenWS(harness.WSTarget{Kind: "service", Project: p.ID, Name: "console"}, 80, 24)
	s2.Send(t, "pong\r")
	s2.Expect(t, "console-got:pong")
	if !strings.Contains(s2.Output(), "console-got:ping") {
		t.Logf("note: scrollback of the service session not replayed on attach")
	}
}

func TestSessions_UnknownTargetsRejected(t *testing.T) {
	t.Parallel()
	p, d := setup(t, "badtarget")
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(10*time.Second))
	defer cancel()
	stream := d.Client().Attach(ctx)
	harness.NoError(t, stream.Send(&v1.AttachRequest{Msg: &v1.AttachRequest_Open{Open: &v1.AttachOpen{
		Target: &v1.AttachTarget{Kind: "terminal", Project: p.ID, SessionId: "no-such-session"}, Cols: 80, Rows: 24,
	}}}), "send open")
	_, err := stream.Receive()
	harness.RequireCode(t, err, connect.CodeNotFound)
	_ = stream.CloseRequest()
	_ = stream.CloseResponse()
}
