package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// AttachWSPath is the dashboard's interactive session endpoint.
const AttachWSPath = "/ws/attach"

// WSTarget mirrors v1.AttachTarget in the websocket JSON protocol.
type WSTarget struct {
	Kind      string `json:"kind"`
	Project   string `json:"project,omitempty"`
	Name      string `json:"name,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// wsMsg is a text (control) frame of the /ws/attach protocol:
//
//	client: {"type":"open","target":{...},"cols":80,"rows":24}
//	        {"type":"resize","cols":..,"rows":..}
//	        {"type":"eof"}    close stdin (non-TTY sessions)
//	        {"type":"close"}  end the session (kills a terminal)
//	server: {"type":"ready","session_id":"...","tty":true,"stdin":true}
//	        {"type":"exit","exit_code":0,"message":"..."}
//	        {"type":"error","message":"..."}
//
// Binary frames carry terminal I/O in both directions.
type wsMsg struct {
	Type      string    `json:"type"`
	Target    *WSTarget `json:"target,omitempty"`
	Cols      int       `json:"cols,omitempty"`
	Rows      int       `json:"rows,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	TTY       bool      `json:"tty,omitempty"`
	Stdin     bool      `json:"stdin,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// WSDialOpts customizes DialWS.
type WSDialOpts struct {
	Client *http.Client // default: an authenticated WebClient
	Origin string       // default: the dashboard's own origin; "-" sends none
	Host   string       // Host header override
	Path   string       // default AttachWSPath
}

// DialWS opens a websocket to the dashboard.
func (d *Daemon) DialWS(ctx context.Context, o WSDialOpts) (*websocket.Conn, *http.Response, error) {
	d.sb.t.Helper()
	if o.Client == nil {
		o.Client = d.WebClient()
	}
	if o.Path == "" {
		o.Path = AttachWSPath
	}
	hdr := http.Header{}
	switch o.Origin {
	case "":
		hdr.Set("Origin", d.sameOrigin())
	case "-":
	default:
		hdr.Set("Origin", o.Origin)
	}
	u := strings.Replace(d.WebURL(), "http://", "ws://", 1) + o.Path
	return websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: o.Client, HTTPHeader: hdr, Host: o.Host})
}

// Session is an interactive session (terminal, task or tty service), over
// the websocket or the Attach RPC.
type Session struct {
	sb        *Sandbox
	out       *outputBuffer
	SessionID string
	TTY       bool
	// Stdin reports whether the session accepts input (websocket only).
	Stdin bool

	send       func(data []byte) error
	resize     func(cols, rows int) error
	closeStdin func() error
	close      func() error // graceful close (kills a terminal)
	drop       func()       // abrupt disconnect (session must survive)

	mu      sync.Mutex
	exit    *int
	exitMsg string
	errs    []string
	paused  chan struct{} // non-nil while reading is paused
	done    chan struct{}
}

func newSession(sb *Sandbox) *Session {
	return &Session{sb: sb, out: newOutputBuffer(), done: make(chan struct{})}
}

// OpenWS opens a session over /ws/attach and waits for "ready".
func (d *Daemon) OpenWS(target WSTarget, cols, rows int) *Session {
	d.sb.t.Helper()
	return d.OpenWSWith(target, cols, rows, WSDialOpts{})
}

// OpenWSWith is OpenWS with dial options.
func (d *Daemon) OpenWSWith(target WSTarget, cols, rows int, o WSDialOpts) *Session {
	sb := d.sb
	sb.t.Helper()
	ctx, cancel := context.WithCancel(sb.ctx)
	dctx, dcancel := ctxWithTimeout(ctx, 20*time.Second)
	conn, resp, err := d.DialWS(dctx, o)
	dcancel()
	if err != nil {
		cancel()
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		sb.t.Fatalf("dial %s: %v (status %d)", AttachWSPath, err, code)
	}
	conn.SetReadLimit(64 << 20)
	s := newSession(sb)
	ready := make(chan wsMsg, 1)
	s.send = func(data []byte) error { return conn.Write(ctx, websocket.MessageBinary, data) }
	s.resize = func(c, r int) error { return writeJSON(ctx, conn, wsMsg{Type: "resize", Cols: c, Rows: r}) }
	s.closeStdin = func() error { return writeJSON(ctx, conn, wsMsg{Type: "eof"}) }
	s.close = func() error { return writeJSON(ctx, conn, wsMsg{Type: "close"}) }
	s.drop = func() { _ = conn.CloseNow(); cancel() }
	go func() {
		defer close(s.done)
		defer s.out.close()
		gotReady := false
		for {
			s.waitUnpaused()
			typ, data, err := conn.Read(ctx)
			if err != nil {
				s.addErr(fmt.Sprintf("read: %v", err))
				return
			}
			if typ == websocket.MessageBinary {
				_, _ = s.out.Write(data)
				continue
			}
			var m wsMsg
			if err := json.Unmarshal(data, &m); err != nil {
				s.addErr(fmt.Sprintf("bad control frame %q: %v", data, err))
				continue
			}
			switch m.Type {
			case "ready":
				if !gotReady {
					gotReady = true
					ready <- m
				}
			case "exit":
				s.setExit(m.ExitCode, m.Message)
			case "error":
				s.addErr("server error: " + m.Message + m.Error)
				if !gotReady {
					gotReady = true
					ready <- m
				}
			default:
				s.addErr("unknown control frame: " + string(data))
			}
		}
	}()
	sb.t.Cleanup(func() {
		s.drop()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			sb.t.Errorf("websocket read loop did not end within 5s of close")
		}
	})
	if err := writeJSON(ctx, conn, wsMsg{Type: "open", Target: &target, Cols: cols, Rows: rows}); err != nil {
		sb.t.Fatalf("send open: %v", err)
	}
	select {
	case m := <-ready:
		if m.Type != "ready" {
			sb.t.Fatalf("open %+v: %s%s", target, m.Message, m.Error)
		}
		s.SessionID, s.TTY, s.Stdin = m.SessionID, m.TTY, m.Stdin
	case <-s.done:
		sb.t.Fatalf("websocket closed before ready: %v", s.Errors())
	case <-time.After(Scale(20 * time.Second)):
		sb.t.Fatalf("no ready frame for %+v within %s", target, Scale(20*time.Second))
	}
	return s
}

func writeJSON(ctx context.Context, conn *websocket.Conn, m wsMsg) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// Attach opens a session over the Attach RPC (as the CLI does).
func (d *Daemon) Attach(target *v1.AttachTarget, cols, rows int) *Session {
	sb := d.sb
	sb.t.Helper()
	ctx, cancel := context.WithCancel(sb.ctx)
	stream := d.Client().Attach(ctx)
	s := newSession(sb)
	var sendMu sync.Mutex
	sendReq := func(r *v1.AttachRequest) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(r)
	}
	s.send = func(data []byte) error {
		return sendReq(&v1.AttachRequest{Msg: &v1.AttachRequest_Input{Input: data}})
	}
	s.resize = func(c, r int) error {
		return sendReq(&v1.AttachRequest{Msg: &v1.AttachRequest_Resize{Resize: &v1.AttachResize{Cols: int32(c), Rows: int32(r)}}})
	}
	s.closeStdin = func() error {
		return sendReq(&v1.AttachRequest{Msg: &v1.AttachRequest_CloseStdin{CloseStdin: true}})
	}
	s.close = func() error {
		if err := sendReq(&v1.AttachRequest{Msg: &v1.AttachRequest_Close{Close: true}}); err != nil {
			return err
		}
		return stream.CloseRequest()
	}
	if err := sendReq(&v1.AttachRequest{Msg: &v1.AttachRequest_Open{Open: &v1.AttachOpen{Target: target, Cols: int32(cols), Rows: int32(rows)}}}); err != nil {
		cancel()
		sb.t.Fatalf("Attach open: %v", err)
	}
	ready := make(chan *v1.AttachReady, 1)
	go func() {
		defer close(s.done)
		defer s.out.close()
		gotReady := false
		for {
			s.waitUnpaused()
			msg, err := stream.Receive()
			if err != nil {
				s.addErr(fmt.Sprintf("receive: %v", err))
				_ = stream.CloseResponse()
				return
			}
			switch m := msg.GetMsg().(type) {
			case *v1.AttachResponse_Ready:
				if !gotReady {
					gotReady = true
					ready <- m.Ready
				}
			case *v1.AttachResponse_Output:
				_, _ = s.out.Write(m.Output)
			case *v1.AttachResponse_Exit:
				code := int(m.Exit.GetExitCode())
				s.setExit(&code, m.Exit.GetMessage())
			}
		}
	}()
	s.drop = func() {
		cancel()
		_ = stream.CloseRequest()
		_ = stream.CloseResponse()
	}
	sb.t.Cleanup(func() {
		s.drop()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			sb.t.Errorf("Attach stream did not end within 5s of cancellation")
		}
	})
	select {
	case r := <-ready:
		s.SessionID, s.TTY = r.GetSessionId(), r.GetTty()
	case <-s.done:
		sb.t.Fatalf("Attach %v ended before ready: %v", target, s.Errors())
	case <-time.After(Scale(20 * time.Second)):
		sb.t.Fatalf("Attach %v: no ready within %s", target, Scale(20*time.Second))
	}
	return s
}

func (s *Session) addErr(e string) {
	s.mu.Lock()
	s.errs = append(s.errs, e)
	s.mu.Unlock()
}

func (s *Session) setExit(code *int, msg string) {
	s.mu.Lock()
	if code == nil {
		zero := 0
		code = &zero
	}
	s.exit = code
	s.exitMsg = msg
	s.mu.Unlock()
}

func (s *Session) waitUnpaused() {
	s.mu.Lock()
	ch := s.paused
	s.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

// Pause stops reading from the connection (to create backpressure).
func (s *Session) Pause() {
	s.mu.Lock()
	if s.paused == nil {
		s.paused = make(chan struct{})
	}
	s.mu.Unlock()
}

// Resume resumes reading.
func (s *Session) Resume() {
	s.mu.Lock()
	if s.paused != nil {
		close(s.paused)
		s.paused = nil
	}
	s.mu.Unlock()
}

// Errors returns transport/protocol errors seen by the read loop.
func (s *Session) Errors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.errs...)
}

// Expect waits for substr in the output after the previous match.
func (s *Session) Expect(t TB, substr string) {
	t.Helper()
	s.ExpectWithin(t, DefaultWait, substr)
}

// ExpectWithin is Expect with an explicit (unscaled) timeout.
func (s *Session) ExpectWithin(t TB, d time.Duration, substr string) {
	t.Helper()
	if err := s.out.expect(substr, Scale(d)); err != nil {
		t.Fatalf("session %s: %v (transport errors: %v)", s.SessionID, err, s.Errors())
	}
}

// Send writes input (use "\r" for Enter on a TTY).
func (s *Session) Send(t TB, input string) {
	t.Helper()
	if err := s.send([]byte(input)); err != nil {
		t.Fatalf("session %s send: %v", s.SessionID, err)
	}
}

// Resize resizes the session's terminal.
func (s *Session) Resize(t TB, cols, rows int) {
	t.Helper()
	if err := s.resize(cols, rows); err != nil {
		t.Fatalf("session resize: %v", err)
	}
}

// CloseStdin sends EOF to the process's stdin (non-TTY sessions).
func (s *Session) CloseStdin(t TB) {
	t.Helper()
	if err := s.closeStdin(); err != nil {
		t.Fatalf("session close stdin: %v", err)
	}
}

// Close closes the session gracefully (a terminal is killed).
func (s *Session) Close(t TB) {
	t.Helper()
	if err := s.close(); err != nil {
		t.Fatalf("session close: %v", err)
	}
}

// Drop disconnects abruptly, without a close message; the server-side
// session must survive and be re-attachable.
func (s *Session) Drop() {
	s.drop()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

// Output returns everything received so far.
func (s *Session) Output() string { return s.out.String() }

// OutputLen returns the number of bytes received so far.
func (s *Session) OutputLen() int { return s.out.Len() }

// Exit returns the exit code once the server reported one.
func (s *Session) Exit() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exit == nil {
		return 0, false
	}
	return *s.exit, true
}

// WaitExit waits for an exit report and returns the code.
func (s *Session) WaitExit(t TB, d time.Duration) int {
	t.Helper()
	var code int
	Eventually(t, "session exit", func(c *C) {
		var ok bool
		if code, ok = s.Exit(); !ok {
			c.Errorf("no exit yet; errors=%v", s.Errors())
		}
	}, Within(d))
	return code
}

// Done is closed when the connection's read loop ends.
func (s *Session) Done() <-chan struct{} { return s.done }
