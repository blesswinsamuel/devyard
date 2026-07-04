package control_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// fakeBackend is an in-memory control.Backend for exercising the server
// without spinning up real child processes.
type fakeBackend struct {
	mu       sync.Mutex
	states   []protocol.ServiceState
	stopErr  error
	logPaths map[string]string
	restarts []string
	stopped  bool
}

func (b *fakeBackend) States() []protocol.ServiceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]protocol.ServiceState, len(b.states))
	copy(out, b.states)
	return out
}

func (b *fakeBackend) Stop(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	return b.stopErr
}

func (b *fakeBackend) Restart(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restarts = append(b.restarts, name)
	return nil
}

func (b *fakeBackend) LogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return p, nil
	}
	return "", errors.New("unknown service " + name)
}

func (b *fakeBackend) restartsFor(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, r := range b.restarts {
		if r == name {
			n++
		}
	}
	return n
}

func newServer(t *testing.T, b *fakeBackend) *control.Server {
	t.Helper()
	// t.TempDir() on macOS lives under /var/folders/... which is long enough
	// to blow past the ~104-char Unix socket path limit. Use a short /tmp
	// dir for the socket itself so tests are stable across platforms.
	sockDir, err := os.MkdirTemp("/tmp", "lc-ctrl")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")
	srv := control.NewServer(sock, b)
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestRoundtripList(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{
			{Name: "api", Status: "running", PID: 123, HasHealth: true, Health: "healthy"},
			{Name: "web", Status: "exited", ExitCode: 0},
		},
	}
	srv := newServer(t, b)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	got, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d states, want 2: %+v", len(got), got)
	}
	if got[0].Name != "api" || got[0].PID != 123 || got[0].Health != "healthy" {
		t.Errorf("api state: %+v", got[0])
	}
	if got[1].Name != "web" || got[1].Status != "exited" {
		t.Errorf("web state: %+v", got[1])
	}
}

func TestRoundtripStop(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !b.stopped {
		t.Errorf("backend.Stop was not called")
	}
}

func TestRoundtripRestartOne(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{
			{Name: "api", Status: "running"},
			{Name: "web", Status: "running"},
		},
	}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Restart("web"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if b.restartsFor("web") != 1 {
		t.Errorf("web restarts = %d, want 1", b.restartsFor("web"))
	}
	if b.restartsFor("api") != 0 {
		t.Errorf("api should not have been restarted")
	}
}

func TestRoundtripRestartAll(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{
			{Name: "api", Status: "running"},
			{Name: "web", Status: "running"},
		},
	}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Restart(""); err != nil {
		t.Fatalf("Restart all: %v", err)
	}
	if b.restartsFor("api") != 1 || b.restartsFor("web") != 1 {
		t.Errorf("restart-all counts: api=%d web=%d, want 1/1",
			b.restartsFor("api"), b.restartsFor("web"))
	}
}

func TestRoundtripLogsNoFollow(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "api.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{logPaths: map[string]string{"api": logPath}}
	srv := newServer(t, b)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	var lines []string
	if err := c.Logs("api", false, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(lines) != 3 || lines[0] != "line1" || lines[2] != "line3" {
		t.Errorf("lines = %v, want [line1 line2 line3]", lines)
	}
}

func TestRoundtripLogsFollow(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "api.log")
	if err := os.WriteFile(logPath, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{logPaths: map[string]string{"api": logPath}}
	srv := newServer(t, b)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	lines := make(chan string, 16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Logs("api", true, func(l string) { lines <- l })
	}()

	// Expect the pre-existing line quickly.
	gotFirst := recvLine(t, lines, 2*time.Second)
	if gotFirst != "first" {
		t.Fatalf("first line = %q, want %q", gotFirst, "first")
	}

	// Append more content; the follow loop should pick it up.
	if err := appendLog(logPath, "second\nthird\n"); err != nil {
		t.Fatal(err)
	}
	gotSecond := recvLine(t, lines, 2*time.Second)
	if gotSecond != "second" {
		t.Fatalf("second line = %q, want %q", gotSecond, "second")
	}
	gotThird := recvLine(t, lines, 2*time.Second)
	if gotThird != "third" {
		t.Fatalf("third line = %q, want %q", gotThird, "third")
	}

	// Closing the server ends the follow stream with Done.
	_ = srv.Close()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Logs returned error after server close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("Logs did not return after server close")
	}
}

func TestLogsUnknownService(t *testing.T) {
	b := &fakeBackend{logPaths: map[string]string{}}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Logs("nope", false, nil); err == nil {
		t.Errorf("Logs for unknown service: expected error, got nil")
	}
}

func TestLogsMissingServiceField(t *testing.T) {
	b := &fakeBackend{logPaths: map[string]string{"api": "/tmp/x"}}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	// Empty service should be rejected by the server.
	if err := c.Send(protocol.Request{Kind: protocol.KindLogs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := c.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Kind != protocol.KindError {
		t.Errorf("response kind = %q, want %q", resp.Kind, protocol.KindError)
	}
}

func TestUnknownRequestKind(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Send(protocol.Request{Kind: "bogus"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := c.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Kind != protocol.KindError {
		t.Errorf("response kind = %q, want %q", resp.Kind, protocol.KindError)
	}
}

func TestDialMissingSocket(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.sock")
	if _, err := control.Dial(missing); err == nil {
		t.Errorf("Dial on missing socket: expected error, got nil")
	}
}

func TestProtocolFrameRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	want := protocol.Response{Kind: protocol.KindLogLine, Line: "hello"}
	if err := protocol.WriteFrame(&buf, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var got protocol.Response
	if err := protocol.ReadFrame(&buf, &got); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Kind != want.Kind || got.Line != want.Line {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestProtocolReadFrameEOF(t *testing.T) {
	var buf bytes.Buffer
	var resp protocol.Response
	err := protocol.ReadFrame(&buf, &resp)
	if !errors.Is(err, io.EOF) {
		t.Errorf("ReadFrame on empty buffer: err = %v, want io.EOF", err)
	}
}

// --- helpers ---

func appendLog(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}

func recvLine(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case l := <-ch:
		return l
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for a log line")
		return ""
	}
}
