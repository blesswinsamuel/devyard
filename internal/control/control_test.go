package control_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	mu         sync.Mutex
	states     []protocol.ServiceState
	stopErr    error
	logPaths   map[string]string
	restarts   []string
	stopped    bool
	stoppedSvc []string
	killedSvc  []string
	killedSigs []string
	stopSvcErr error
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

func (b *fakeBackend) StopService(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stoppedSvc = append(b.stoppedSvc, name)
	return b.stopSvcErr
}

func (b *fakeBackend) KillService(name, signal string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killedSvc = append(b.killedSvc, name)
	b.killedSigs = append(b.killedSigs, signal)
	return nil
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

func (b *fakeBackend) Top(name string) ([]protocol.ServiceStat, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]protocol.ServiceStat, 0, len(b.states))
	for _, st := range b.states {
		if name != "" && st.Name != name {
			continue
		}
		out = append(out, protocol.ServiceStat{
			Name:     st.Name,
			Status:   st.Status,
			PID:      st.PID,
			PGID:     st.PID,
			Procs:    1,
			CPU:      12.5,
			RSSBytes: 4096,
		})
	}
	return out, nil
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

func (b *fakeBackend) stoppedCount(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.stoppedSvc {
		if s == name {
			n++
		}
	}
	return n
}

func (b *fakeBackend) killedCount(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.killedSvc {
		if s == name {
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
	srv := control.NewServer(sock, control.SingleProjectBackend{Backend: b})
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

	got, err := c.List("")
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
	if err := c.Stop(""); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !b.stopped {
		t.Errorf("backend.Stop was not called")
	}
}

func TestRoundtripStopService(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.StopService("", "api"); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	if b.stoppedCount("api") != 1 {
		t.Errorf("api stops = %d, want 1", b.stoppedCount("api"))
	}
}

func TestStopServiceRequiresName(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Send(protocol.Request{Kind: protocol.KindStopService}); err != nil {
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

func TestRoundtripKillService(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.KillService("", "api", "SIGTERM"); err != nil {
		t.Fatalf("KillService: %v", err)
	}
	if b.killedCount("api") != 1 {
		t.Errorf("api kills = %d, want 1", b.killedCount("api"))
	}
	if len(b.killedSigs) != 1 || b.killedSigs[0] != "SIGTERM" {
		t.Errorf("signals = %v, want [SIGTERM]", b.killedSigs)
	}
}

func TestKillServiceEmptyServiceKillsAll(t *testing.T) {
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
	if err := c.KillService("", "", "SIGKILL"); err != nil {
		t.Fatalf("KillService all: %v", err)
	}
	if b.killedCount("api") != 1 || b.killedCount("web") != 1 {
		t.Errorf("kill-all counts: api=%d web=%d, want 1/1",
			b.killedCount("api"), b.killedCount("web"))
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
	if err := c.Restart("", "web"); err != nil {
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
	if err := c.Restart("", ""); err != nil {
		t.Fatalf("Restart all: %v", err)
	}
	if b.restartsFor("api") != 1 || b.restartsFor("web") != 1 {
		t.Errorf("restart-all counts: api=%d web=%d, want 1/1",
			b.restartsFor("api"), b.restartsFor("web"))
	}
}

func TestRoundtripTop(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{
			{Name: "api", Status: "running", PID: 123},
			{Name: "web", Status: "running", PID: 456},
		},
	}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	got, err := c.Top("", "")
	if err != nil {
		t.Fatalf("Top: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d stats, want 2: %+v", len(got), got)
	}
	if got[0].Name != "api" || got[0].PGID != 123 || got[0].CPU != 12.5 || got[0].RSSBytes != 4096 {
		t.Errorf("api stat: %+v", got[0])
	}
	if got[1].Name != "web" || got[1].Procs != 1 {
		t.Errorf("web stat: %+v", got[1])
	}
}

func TestRoundtripTopOneService(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{
			{Name: "api", Status: "running", PID: 123},
			{Name: "web", Status: "running", PID: 456},
		},
	}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	got, err := c.Top("", "web")
	if err != nil {
		t.Fatalf("Top: %v", err)
	}
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("got %+v, want only the web stat", got)
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
	if err := c.Logs("", "api", false, func(l string) { lines = append(lines, l) }); err != nil {
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
		errCh <- c.Logs("", "api", true, func(l string) { lines <- l })
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
	if err := c.Logs("", "nope", false, nil); err == nil {
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

// --- multi-backend tests ---

// fakeMultiBackend is an in-memory control.MultiBackend for exercising the
// server's daemon-level dispatch (list_projects, start_project, stop_project,
// stop_daemon) and per-project routing.
type fakeMultiBackend struct {
	mu           sync.Mutex
	projects     map[string]control.Backend
	started      []string
	stopped      []string
	daemonStopCh chan struct{}
}

func newFakeMultiBackend() *fakeMultiBackend {
	return &fakeMultiBackend{
		projects:     make(map[string]control.Backend),
		daemonStopCh: make(chan struct{}),
	}
}

func (m *fakeMultiBackend) ListProjects() []protocol.ProjectInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]protocol.ProjectInfo, 0, len(m.projects))
	for name, b := range m.projects {
		status := "running"
		if fb, ok := b.(*fakeBackend); ok {
			fb.mu.Lock()
			if fb.stopped {
				status = "stopped"
			}
			fb.mu.Unlock()
		}
		out = append(out, protocol.ProjectInfo{Name: name, Status: status})
	}
	return out
}

func (m *fakeMultiBackend) StartProject(configPath string, build bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = append(m.started, configPath)
	m.projects[filepath.Base(filepath.Dir(configPath))] = &fakeBackend{}
	return nil
}

func (m *fakeMultiBackend) StopProject(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.projects[name]
	if !ok {
		return fmt.Errorf("project %q not running", name)
	}
	m.stopped = append(m.stopped, name)
	return b.Stop(context.Background())
}

func (m *fakeMultiBackend) RemoveProject(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[name]; !ok {
		return fmt.Errorf("project %q not running", name)
	}
	delete(m.projects, name)
	return nil
}

func (m *fakeMultiBackend) StopDaemon() error {
	select {
	case <-m.daemonStopCh:
	default:
		close(m.daemonStopCh)
	}
	return nil
}

func (m *fakeMultiBackend) ProjectBackend(project string) (control.Backend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q not running", project)
	}
	return b, nil
}

func newMultiServer(t *testing.T, m control.MultiBackend) *control.Server {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "lc-ctrl")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")
	srv := control.NewServer(sock, m)
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestMultiListProjects(t *testing.T) {
	m := newFakeMultiBackend()
	m.projects["api"] = &fakeBackend{}
	m.projects["web"] = &fakeBackend{}
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	projects, err := c.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2: %+v", len(projects), projects)
	}
}

func TestMultiStartProject(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.StartProject("/path/to/local-compose.yml", false); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if len(m.started) != 1 || m.started[0] != "/path/to/local-compose.yml" {
		t.Errorf("started = %v, want [/path/to/local-compose.yml]", m.started)
	}
}

func TestMultiStopProject(t *testing.T) {
	m := newFakeMultiBackend()
	m.projects["api"] = &fakeBackend{}
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.StopProject("api"); err != nil {
		t.Fatalf("StopProject: %v", err)
	}
	if len(m.stopped) != 1 || m.stopped[0] != "api" {
		t.Errorf("stopped = %v, want [api]", m.stopped)
	}
}

func TestMultiRemoveProject(t *testing.T) {
	m := newFakeMultiBackend()
	m.projects["api"] = &fakeBackend{}
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.RemoveProject("api"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if _, exists := m.projects["api"]; exists {
		t.Errorf("expected project 'api' to be removed")
	}
}

func TestMultiStopDaemon(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.StopDaemon(); err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}
	select {
	case <-m.daemonStopCh:
	default:
		t.Fatalf("daemon stop channel was not closed")
	}
}

func TestMultiProjectBackendRouting(t *testing.T) {
	b := &fakeBackend{
		states: []protocol.ServiceState{{Name: "svc", Status: "running", PID: 42}},
	}
	m := newFakeMultiBackend()
	m.projects["api"] = b
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	got, err := c.List("api")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "svc" || got[0].PID != 42 {
		t.Errorf("states = %+v, want [{svc 42}]", got)
	}
}

func TestMultiProjectBackendUnknownProject(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err = c.List("nope")
	if err == nil {
		t.Fatalf("List for unknown project: expected error, got nil")
	}
}

func TestStopDaemonRequiresNoFields(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Send(protocol.Request{Kind: protocol.KindStopDaemon}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := c.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Kind != protocol.KindDone {
		t.Errorf("response kind = %q, want %q", resp.Kind, protocol.KindDone)
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
