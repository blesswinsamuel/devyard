package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/control"
	localcomposev1 "github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1"
	"github.com/blesswinsamuel/local-compose/internal/gen/proto/localcompose/v1/localcomposev1connect"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/web"
)

// fakeBackend is an in-memory control.Backend for exercising the daemon-side
// control server behind the web proxy.
type fakeBackend struct {
	mu             sync.Mutex
	states         []*protocol.ServiceState
	logPaths       map[string]string
	actions        []*protocol.ActionState
	actionLogPaths map[string]string
	restarts       []string
	stopped        []string
	started        []string
	killed         []string
	runActionFn    func(name string, args []string, out io.Writer) (int, error)
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		logPaths:       make(map[string]string),
		actionLogPaths: make(map[string]string),
	}
}

func (b *fakeBackend) States() []*protocol.ServiceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.ServiceState, len(b.states))
	copy(out, b.states)
	return out
}

func (b *fakeBackend) ActionStates() []*protocol.ActionState { return nil }

func (b *fakeBackend) Stop(context.Context) error { return nil }

func (b *fakeBackend) StopService(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = append(b.stopped, name)
	return nil
}

func (b *fakeBackend) StartService(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started = append(b.started, name)
	return nil
}

func (b *fakeBackend) KillService(name, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killed = append(b.killed, name)
	return nil
}

func (b *fakeBackend) Restart(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.restarts = append(b.restarts, name)
	return nil
}

func (b *fakeBackend) Top(string) ([]*protocol.ServiceStat, error) { return nil, nil }

func (b *fakeBackend) ListPorts() ([]*protocol.PortBinding, error) { return nil, nil }

func (b *fakeBackend) LogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("unknown service %q", name)
}

func (b *fakeBackend) PreviousLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return strings.TrimSuffix(p, ".log") + ".prev.log", nil
	}
	return "", fmt.Errorf("unknown service %q", name)
}

func (b *fakeBackend) ListActions() []*protocol.ActionState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.ActionState, len(b.actions))
	copy(out, b.actions)
	return out
}

func (b *fakeBackend) RunAction(_ context.Context, name string, args []string, out io.Writer) (int, error) {
	b.mu.Lock()
	fn := b.runActionFn
	b.mu.Unlock()
	if fn != nil {
		return fn(name, args, out)
	}
	return 0, nil
}

func (b *fakeBackend) ActionLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.actionLogPaths[name]
	if !ok {
		return "", fmt.Errorf("action %q has no log file yet", name)
	}
	return p, nil
}

func (b *fakeBackend) ActionPreviousLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.actionLogPaths[name]
	if !ok {
		return "", fmt.Errorf("unknown action %q", name)
	}
	return strings.TrimSuffix(p, ".log") + ".prev.log", nil
}

// fakeMultiBackend is an in-memory control.MultiBackend.
type fakeMultiBackend struct {
	mu         sync.Mutex
	projects   map[string]*projectEntry
	onState    func(project string, state *protocol.ServiceState)
	onAction   func(project string, state *protocol.ActionState)
	onGit      func(project string)
	onProjects func()
	daemonStop chan struct{}
}

type projectEntry struct {
	backend    *fakeBackend
	configPath string
}

func newFakeMulti() *fakeMultiBackend {
	return &fakeMultiBackend{
		projects:   make(map[string]*projectEntry),
		daemonStop: make(chan struct{}),
	}
}

func (m *fakeMultiBackend) addProject(t *testing.T, name, configPath string, b *fakeBackend) {
	t.Helper()
	if configPath == "" {
		configPath = filepath.Join(t.TempDir(), "local-compose.yml")
	}
	m.mu.Lock()
	m.projects[name] = &projectEntry{backend: b, configPath: configPath}
	m.mu.Unlock()
}

func (m *fakeMultiBackend) ListProjects() []*protocol.ProjectInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.projects))
	for name := range m.projects {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]*protocol.ProjectInfo, 0, len(names))
	for _, name := range names {
		e := m.projects[name]
		total, runningCount := 0, 0
		for _, st := range e.backend.States() {
			total++
			if st.Status == "running" {
				runningCount++
			}
		}
		status := "stopped"
		if total == 0 || runningCount > 0 {
			status = "running"
		}
		out = append(out, &protocol.ProjectInfo{
			Name:            name,
			Status:          status,
			ConfigPath:      e.configPath,
			RunningServices: int32(runningCount),
			TotalServices:   int32(total),
		})
	}
	return out
}

func (m *fakeMultiBackend) StartProject(configPath string, _ bool, _ string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := filepath.Base(filepath.Dir(configPath))
	m.projects[name] = &projectEntry{backend: newFakeBackend(), configPath: configPath}
	return nil
}

func (m *fakeMultiBackend) StopProject(name string) error {
	m.mu.Lock()
	e, ok := m.projects[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q not running", name)
	}
	return e.backend.Stop(context.Background())
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

func (m *fakeMultiBackend) StartService(project, service string) error {
	m.mu.Lock()
	e, ok := m.projects[project]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("project %q not running", project)
	}
	return e.backend.StartService(service)
}

func (m *fakeMultiBackend) StopDaemon() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.daemonStop:
	default:
		close(m.daemonStop)
	}
	return nil
}

func (m *fakeMultiBackend) DaemonStatus() (*protocol.DaemonInfo, error) {
	return &protocol.DaemonInfo{
		Pid:        1234,
		StartTime:  protocol.TimeToProto(time.Now()),
		Goroutines: 10,
		GoVersion:  "go1.26.0",
	}, nil
}

func (m *fakeMultiBackend) RestartDaemon(restartServices bool) error { return m.StopDaemon() }

func (m *fakeMultiBackend) ProjectBackend(project string) (control.Backend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	return e.backend, nil
}

func (m *fakeMultiBackend) ListServices(project string) ([]*protocol.ServiceState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if project != "" {
		e, ok := m.projects[project]
		if !ok {
			return nil, fmt.Errorf("project %q is not running", project)
		}
		states := e.backend.States()
		for _, st := range states {
			st.Project = project
		}
		return states, nil
	}
	var all []*protocol.ServiceState
	for name, e := range m.projects {
		states := e.backend.States()
		for _, st := range states {
			st.Project = name
		}
		all = append(all, states...)
	}
	return all, nil
}

func (m *fakeMultiBackend) ListActions(project string) ([]*protocol.ActionState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if project != "" {
		e, ok := m.projects[project]
		if !ok {
			return nil, fmt.Errorf("project %q is not running", project)
		}
		actions := e.backend.ListActions()
		for _, act := range actions {
			act.Project = project
		}
		return actions, nil
	}
	var all []*protocol.ActionState
	for name, e := range m.projects {
		actions := e.backend.ListActions()
		for _, act := range actions {
			act.Project = name
		}
		all = append(all, actions...)
	}
	return all, nil
}

func (m *fakeMultiBackend) SetOnStateChange(fn func(string, *protocol.ServiceState)) {
	m.mu.Lock()
	m.onState = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) SetOnActionStateChange(fn func(string, *protocol.ActionState)) {
	m.mu.Lock()
	m.onAction = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) SetOnGitChange(fn func(string)) {
	m.mu.Lock()
	m.onGit = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) SetOnProjectsChange(fn func()) {
	m.mu.Lock()
	m.onProjects = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) GitLog(string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	commits := []*protocol.GitCommit{{Hash: "abc123", Short: "abc123", Subject: "initial"}}
	return commits, nil, nil, nil, nil
}

func (m *fakeMultiBackend) GitDiff(_ string, hash string, _ string, _ ...int) (*protocol.GitDiffResult, error) {
	return &protocol.GitDiffResult{Commit: &protocol.GitCommit{Hash: hash}, Diff: "diff"}, nil
}

func (m *fakeMultiBackend) GitCommit(string, string) error            { return nil }
func (m *fakeMultiBackend) GitStage(string, string, bool, bool) error { return nil }
func (m *fakeMultiBackend) GitPush(string) (string, error)            { return "pushed", nil }
func (m *fakeMultiBackend) GitPull(string) (string, error)            { return "pulled", nil }
func (m *fakeMultiBackend) GitFetch(string) (string, error)           { return "fetched", nil }
func (m *fakeMultiBackend) ListPorts(project string) ([]*protocol.PortBinding, error) {
	m.mu.Lock()
	_, ok := m.projects[project]
	m.mu.Unlock()
	if project != "" && !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	return []*protocol.PortBinding{{
		Project: project, Service: "web", Pid: 42,
		Ip: "127.0.0.1", Port: 3000, Protocol: "tcp",
	}}, nil
}

func (m *fakeMultiBackend) fireState(project string, state *protocol.ServiceState) {
	m.mu.Lock()
	fn := m.onState
	m.mu.Unlock()
	if fn != nil {
		fn(project, state)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func startStack(t *testing.T, m *fakeMultiBackend) *web.Server {
	t.Helper()
	sockDir, err := os.MkdirTemp("/tmp", "lc-web")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "daemon.sock")

	ctrlSrv := control.NewServer(sock, m)
	if err := ctrlSrv.ListenAndServe(); err != nil {
		t.Fatalf("control.ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = ctrlSrv.Close() })

	webSrv := web.NewServer("127.0.0.1:0", sock)
	if err := webSrv.ListenAndServe(); err != nil {
		t.Fatalf("web.ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = webSrv.Close() })

	return webSrv
}

func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := fmt.Sprintf("ws://%s/ws", addr)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func sendWSMsg(t *testing.T, c *websocket.Conn, msg any) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal msg: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write ws: %v", err)
	}
}

func recvWSMsgTimeout(t *testing.T, c *websocket.Conn, timeout time.Duration) (map[string]any, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal ws (%s): %v", data, err)
	}
	return out, true
}

func expectType(t *testing.T, c *websocket.Conn, wantType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := recvWSMsgTimeout(t, c, time.Until(deadline))
		if !ok {
			break
		}
		if msg["type"] == wantType {
			return msg
		}
	}
	t.Fatalf("timed out waiting for message type %q", wantType)
	return nil
}

// --- static assets ---

func TestWebServesEmbeddedSPA(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	resp, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!doctype html>") && !strings.Contains(string(body), "<html") {
		t.Fatalf("unexpected index.html body: %s", body)
	}
}

func TestWebSPAFallback(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	resp, err := http.Get("http://" + srv.Addr() + "/projects/api/logs")
	if err != nil {
		t.Fatalf("GET /projects/api/logs: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html fallback", ct)
	}
}

// --- ConnectRPC Proxying ---

func TestConnectRPCProxy(t *testing.T) {
	m := newFakeMulti()
	b := newFakeBackend()
	b.states = []*protocol.ServiceState{{Name: "svc", Status: "running"}}
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	rpcClient := localcomposev1connect.NewDaemonServiceClient(http.DefaultClient, "http://"+srv.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. ListProjects
	res, err := rpcClient.ListProjects(ctx, connect.NewRequest(&localcomposev1.ListProjectsRequest{}))
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(res.Msg.Projects) != 1 || res.Msg.Projects[0].Name != "api" {
		t.Fatalf("unexpected projects: %+v", res.Msg.Projects)
	}

	// 2. StartService
	_, err = rpcClient.StartService(ctx, connect.NewRequest(&localcomposev1.StartServiceRequest{
		Project: "api",
		Service: "web",
	}))
	if err != nil {
		t.Fatalf("StartService: %v", err)
	}
	if len(b.started) != 1 || b.started[0] != "web" {
		t.Fatalf("started = %v, want ['web']", b.started)
	}

	// 3. SubscribeEvents stream
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		m.fireState("api", &protocol.ServiceState{Name: "web", Status: "running"})
	}()

	stream, err := rpcClient.SubscribeEvents(streamCtx, connect.NewRequest(&localcomposev1.SubscribeEventsRequest{}))
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}

	// First event is the immediate heartbeat (flushes response headers).
	if !stream.Receive() {
		t.Fatalf("expected initial heartbeat, got error: %v", stream.Err())
	}
	if stream.Msg().GetHeartbeat() == nil {
		t.Fatalf("expected heartbeat event first, got: %+v", stream.Msg())
	}

	// Second event is the service state change.
	if stream.Receive() {
		ev := stream.Msg()
		sc := ev.GetServiceStateChanged()
		if sc == nil || sc.Project != "api" || sc.State.Name != "web" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	} else {
		t.Fatalf("stream receive error: %v", stream.Err())
	}
}

// --- Terminals over WebSocket ---

func TestWSSpawnTerminalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := newFakeMulti()
	m.addProject(t, "api", filepath.Join(dir, "local-compose.yml"), newFakeBackend())
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "spawn_terminal", "id": "t1", "project": "api", "cols": 80, "rows": 24})

	sendWSMsg(t, c, map[string]any{"type": "terminal_input", "id": "t1", "data": "echo hello_pty\n"})

	var blob strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := recvWSMsgTimeout(t, c, time.Until(deadline))
		if !ok {
			break
		}
		switch msg["type"] {
		case "error":
			t.Fatalf("terminal error: %v", msg["error"])
		case "terminal_output":
			fmt.Fprintf(&blob, "%v", msg["output"])
			if strings.Contains(blob.String(), "hello_pty") {
				sendWSMsg(t, c, map[string]any{"type": "close_terminal", "id": "t1"})
				return
			}
		}
	}
	t.Fatalf("never saw hello_pty in terminal output; got %q", blob.String())
}

func TestWSSpawnTerminalUnknownProject(t *testing.T) {
	m := newFakeMulti()
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "spawn_terminal", "id": "t1"})
	resp := expectType(t, c, "error")
	if !strings.Contains(fmt.Sprint(resp["error"]), "project is required") {
		t.Fatalf("resp = %+v, want required-project error", resp)
	}

	sendWSMsg(t, c, map[string]string{"type": "spawn_terminal", "id": "t2", "project": "ghost"})
	resp = expectType(t, c, "error")
	if !strings.Contains(fmt.Sprint(resp["error"]), "unknown project") {
		t.Fatalf("resp = %+v, want unknown-project error", resp)
	}
}

func TestWSTerminalInputUnknownSessionErrors(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "terminal_input", "id": "ghost", "data": "x"})
	resp := expectType(t, c, "error")
	if !strings.Contains(fmt.Sprint(resp["error"]), "not found") {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWSSpawnTerminalReattachAndHistory(t *testing.T) {
	dir := t.TempDir()
	m := newFakeMulti()
	m.addProject(t, "api", filepath.Join(dir, "local-compose.yml"), newFakeBackend())
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "spawn_terminal", "id": "t1", "project": "api", "cols": 80, "rows": 24})
	sendWSMsg(t, c, map[string]any{"type": "terminal_input", "id": "t1", "data": "echo unique_history_token\n"})

	// Wait for output on first connection
	var blob strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := recvWSMsgTimeout(t, c, time.Until(deadline))
		if !ok {
			break
		}
		if msg["type"] == "terminal_output" {
			fmt.Fprintf(&blob, "%v", msg["output"])
			if strings.Contains(blob.String(), "unique_history_token") {
				break
			}
		}
	}
	if !strings.Contains(blob.String(), "unique_history_token") {
		t.Fatalf("did not see unique_history_token on first spawn: %q", blob.String())
	}

	// Now simulate client remount / tab switch: re-spawn the same ID without closing it
	sendWSMsg(t, c, map[string]any{"type": "spawn_terminal", "id": "t1", "project": "api", "cols": 100, "rows": 30})

	var reattachBlob strings.Builder
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, ok := recvWSMsgTimeout(t, c, time.Until(deadline))
		if !ok {
			break
		}
		if msg["type"] == "terminal_output" {
			fmt.Fprintf(&reattachBlob, "%v", msg["output"])
			if strings.Contains(reattachBlob.String(), "unique_history_token") {
				sendWSMsg(t, c, map[string]any{"type": "close_terminal", "id": "t1"})
				return
			}
		}
	}
	t.Fatalf("reattach did not replay history; got %q", reattachBlob.String())
}
