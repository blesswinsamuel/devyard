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

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/web"
)

// fakeBackend is an in-memory control.Backend for exercising the daemon-side
// control server behind the web bridge.
type fakeBackend struct {
	mu             sync.Mutex
	states         []protocol.ServiceState
	logPaths       map[string]string
	actions        []protocol.ActionInfo
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

func (b *fakeBackend) States() []protocol.ServiceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]protocol.ServiceState, len(b.states))
	copy(out, b.states)
	return out
}

func (b *fakeBackend) ActionStates() []protocol.ActionState { return nil }

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

func (b *fakeBackend) Top(string) ([]protocol.ServiceStat, error) { return nil, nil }

func (b *fakeBackend) Ports() ([]protocol.PortBinding, error) { return nil, nil }

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

func (b *fakeBackend) ListActions() []protocol.ActionInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]protocol.ActionInfo, len(b.actions))
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

// setActionLogPath registers an action's log path (simulating a first run
// creating the log file).
func (b *fakeBackend) setActionLogPath(action, path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.actionLogPaths[action] = path
}

// fakeMultiBackend is an in-memory control.MultiBackend.
type fakeMultiBackend struct {
	mu         sync.Mutex
	projects   map[string]*projectEntry
	onState    func(project string, state protocol.ServiceState)
	onAction   func(project string, state protocol.ActionState)
	onGit      func(project string)
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

func (m *fakeMultiBackend) ListProjects() []protocol.ProjectInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.projects))
	for name := range m.projects {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]protocol.ProjectInfo, 0, len(names))
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
		out = append(out, protocol.ProjectInfo{
			Name:            name,
			Status:          status,
			ConfigPath:      e.configPath,
			RunningServices: runningCount,
			TotalServices:   total,
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
		PID:        1234,
		StartTime:  time.Now(),
		Goroutines: 10,
		GoVersion:  "go1.26.0",
	}, nil
}

func (m *fakeMultiBackend) RestartDaemon() error { return m.StopDaemon() }

func (m *fakeMultiBackend) ProjectBackend(project string) (control.Backend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	return e.backend, nil
}

func (m *fakeMultiBackend) SetOnStateChange(fn func(string, protocol.ServiceState)) {
	m.mu.Lock()
	m.onState = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) SetOnActionStateChange(fn func(string, protocol.ActionState)) {
	m.mu.Lock()
	m.onAction = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) SetOnGitChange(fn func(string)) {
	m.mu.Lock()
	m.onGit = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) GitLog(string) ([]protocol.GitCommit, []protocol.GitBranch, []protocol.GitTag, []protocol.GitStash, error) {
	commits := []protocol.GitCommit{{Hash: "abc123", Short: "abc123", Subject: "initial"}}
	return commits, nil, nil, nil, nil
}

func (m *fakeMultiBackend) GitDiff(_ string, hash string, _ ...int) (*protocol.GitDiffResult, error) {
	return &protocol.GitDiffResult{Commit: protocol.GitCommit{Hash: hash}, Diff: "diff"}, nil
}

func (m *fakeMultiBackend) GitCommit(string, string) error            { return nil }
func (m *fakeMultiBackend) GitStage(string, string, bool, bool) error { return nil }
func (m *fakeMultiBackend) GitPush(string) (string, error)            { return "pushed", nil }
func (m *fakeMultiBackend) GitPull(string) (string, error)            { return "pulled", nil }
func (m *fakeMultiBackend) GitFetch(string) (string, error)           { return "fetched", nil }

func (m *fakeMultiBackend) Ports(project string) ([]protocol.PortBinding, error) {
	m.mu.Lock()
	_, ok := m.projects[project]
	m.mu.Unlock()
	if project != "" && !ok {
		return nil, fmt.Errorf("project %q is not running", project)
	}
	return []protocol.PortBinding{{
		Project: project, Service: "web", PID: 42,
		IP: "127.0.0.1", Port: 3000, Protocol: "tcp",
	}}, nil
}

// fireState simulates a supervisor state change notification.
func (m *fakeMultiBackend) fireState(project string, state protocol.ServiceState) {
	m.mu.Lock()
	fn := m.onState
	m.mu.Unlock()
	if fn != nil {
		fn(project, state)
	}
}

// fireActionState simulates an action state change notification.
func (m *fakeMultiBackend) fireActionState(project string, state protocol.ActionState) {
	m.mu.Lock()
	fn := m.onAction
	m.mu.Unlock()
	if fn != nil {
		fn(project, state)
	}
}

// fireGit simulates a repository change notification.
func (m *fakeMultiBackend) fireGit(project string) {
	m.mu.Lock()
	fn := m.onGit
	m.mu.Unlock()
	if fn != nil {
		fn(project)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// startStack boots a real control.Server on a temp Unix socket backed by the
// given fake multi backend, plus a web server proxying to it — exercising the
// exact production path of every WS message.
func startStack(t *testing.T, m *fakeMultiBackend) *web.Server {
	t.Helper()
	// t.TempDir() paths can exceed the ~104-char Unix socket limit on macOS;
	// use a short /tmp dir for the socket itself.
	sockDir, err := os.MkdirTemp("/tmp", "lc-web")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")

	ctrl := control.NewServer(sock, m)
	if err := ctrl.ListenAndServe(); err != nil {
		t.Fatalf("control ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })

	srv := web.NewServer("127.0.0.1:0", sock)
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("web ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://%s/ws", addr), nil)
	if err != nil {
		t.Fatalf("Dial WS: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func sendWSMsg(t *testing.T, c *websocket.Conn, msg any) {
	t.Helper()
	data, _ := json.Marshal(msg)
	if err := c.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatalf("Write: %v", err)
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
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return m, true
}

// expectType reads messages until one of the wanted type arrives (skipping
// unrelated pushes such as events), failing on timeout with what was seen.
func expectType(t *testing.T, c *websocket.Conn, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var seen []string
	for {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			break
		}
		msg, ok := recvWSMsgTimeout(t, c, timeout)
		if !ok {
			break
		}
		typeName, _ := msg["type"].(string)
		if typeName == want {
			return msg
		}
		seen = append(seen, typeName)
	}
	t.Fatalf("no %q message received (saw %v)", want, seen)
	return nil
}

// --- HTTP tests ---

func TestHTTPIndex(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
}

func TestHTTPSPAFallback(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	resp, err := http.Get(fmt.Sprintf("http://%s/projects/api/services/web", srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html fallback", ct)
	}
}

// --- query dispatch tests ---

func TestWSListProjects(t *testing.T) {
	m := newFakeMulti()
	b := newFakeBackend()
	b.states = []protocol.ServiceState{{Name: "svc", Status: "running"}}
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "list_projects"})
	resp := expectType(t, c, "projects")
	data, _ := json.Marshal(resp["data"])
	var projects []protocol.ProjectInfo
	if err := json.Unmarshal(data, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "api" || projects[0].Status != "running" {
		t.Fatalf("projects = %+v", projects)
	}
	if projects[0].ConfigPath == "" {
		t.Errorf("config_path should be forwarded so terminals resolve cwd")
	}
}

func TestWSListServicesUnknownProject(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "list_services", "project": "nope"})
	resp := expectType(t, c, "error")
	if resp["error"] == "" {
		t.Fatalf("expected error text, got %+v", resp)
	}
}

func TestWSServiceOps(t *testing.T) {
	tests := []struct {
		msg   map[string]any
		check func(b *fakeBackend)
	}{
		{
			msg: map[string]any{"type": "restart_service", "project": "api", "service": "web"},
			check: func(b *fakeBackend) {
				if len(b.restarts) != 1 || b.restarts[0] != "web" {
					t.Errorf("restarts = %v", b.restarts)
				}
			},
		},
		{
			msg: map[string]any{"type": "stop_service", "project": "api", "service": "web"},
			check: func(b *fakeBackend) {
				if len(b.stopped) != 1 || b.stopped[0] != "web" {
					t.Errorf("stopped = %v", b.stopped)
				}
			},
		},
		{
			msg: map[string]any{"type": "start_service", "project": "api", "service": "web"},
			check: func(b *fakeBackend) {
				if len(b.started) != 1 || b.started[0] != "web" {
					t.Errorf("started = %v", b.started)
				}
			},
		},
		{
			msg: map[string]any{"type": "kill_service", "project": "api", "service": "web", "signal": "SIGTERM"},
			check: func(b *fakeBackend) {
				if len(b.killed) != 1 || b.killed[0] != "web" {
					t.Errorf("killed = %v", b.killed)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.msg["type"].(string), func(t *testing.T) {
			b := newFakeBackend()
			b.states = []protocol.ServiceState{{Name: "web", Status: "running"}}
			m := newFakeMulti()
			m.addProject(t, "api", "", b)
			srv := startStack(t, m)

			c := dialWS(t, srv.Addr())
			sendWSMsg(t, c, tt.msg)
			resp := expectType(t, c, "result")
			if resp["ok"] != true {
				t.Fatalf("resp = %+v, want ok=true", resp)
			}
			tt.check(b)
		})
	}
}

func TestWSStartProjectResolvesConfigPathByName(t *testing.T) {
	m := newFakeMulti()
	srv := startStack(t, m)

	// Prime the registry exactly like a prior `up` would have.
	m.addProject(t, "api", "/somewhere/api/local-compose.yml", newFakeBackend())

	c := dialWS(t, srv.Addr())
	// No config_path: the bridge must resolve it from the registered project.
	sendWSMsg(t, c, map[string]any{"type": "start_project", "project": "api"})
	resp := expectType(t, c, "result")
	if resp["ok"] != true {
		t.Fatalf("resp = %+v, want ok=true", resp)
	}

	// A truly unknown project without config_path must fail clearly.
	sendWSMsg(t, c, map[string]any{"type": "start_project", "project": "ghost"})
	resp = expectType(t, c, "result")
	if resp["ok"] != false || resp["error"] == "" {
		t.Fatalf("resp = %+v, want ok=false with error", resp)
	}
}

func TestWSUnknownType(t *testing.T) {
	srv := startStack(t, newFakeMulti())
	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "bogus"})
	resp := expectType(t, c, "error")
	if !strings.Contains(fmt.Sprint(resp["error"]), "unknown message type") {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWSDaemonStatusAndPorts(t *testing.T) {
	m := newFakeMulti()
	m.addProject(t, "api", "", newFakeBackend())
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())

	sendWSMsg(t, c, map[string]string{"type": "daemon_status"})
	resp := expectType(t, c, "daemon_status")
	data, _ := json.Marshal(resp["data"])
	var info protocol.DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil || info.PID != 1234 {
		t.Fatalf("daemon_status data = %s (%v)", data, err)
	}

	sendWSMsg(t, c, map[string]string{"type": "list_ports", "project": "api"})
	resp = expectType(t, c, "ports")
	data, _ = json.Marshal(resp["data"])
	var ports []protocol.PortBinding
	if err := json.Unmarshal(data, &ports); err != nil || len(ports) != 1 || ports[0].Port != 3000 {
		t.Fatalf("ports data = %s (%v)", data, err)
	}
}

func TestWSActions(t *testing.T) {
	m := newFakeMulti()
	b := newFakeBackend()
	b.actions = []protocol.ActionInfo{{Name: "migrate", Command: "bin/migrate"}}
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())

	sendWSMsg(t, c, map[string]string{"type": "list_actions", "project": "api"})
	resp := expectType(t, c, "actions")
	data, _ := json.Marshal(resp["data"])
	var actions []protocol.ActionInfo
	if err := json.Unmarshal(data, &actions); err != nil || len(actions) != 1 || actions[0].Name != "migrate" {
		t.Fatalf("actions data = %s (%v)", data, err)
	}

	sendWSMsg(t, c, map[string]string{"type": "list_action_states", "project": "api"})
	expectType(t, c, "action_states")

	// run_action returns the action's exit code via action_done.
	b.runActionFn = func(name string, _ []string, w io.Writer) (int, error) {
		if name != "migrate" {
			return 0, fmt.Errorf("action %q not found", name)
		}
		_, _ = fmt.Fprintf(w, "migrating\n")
		return 3, nil
	}
	sendWSMsg(t, c, map[string]any{"type": "run_action", "project": "api", "action": "migrate"})
	resp = expectType(t, c, "action_done")
	if resp["ok"] != true {
		t.Fatalf("action_done resp = %+v, want ok=true", resp)
	}
	if code, _ := resp["exit_code"].(float64); int(code) != 3 {
		t.Fatalf("exit_code = %v, want 3", resp["exit_code"])
	}

	// Unknown actions surface as failed action_done, not a bare error.
	sendWSMsg(t, c, map[string]any{"type": "run_action", "project": "api", "action": "nope"})
	resp = expectType(t, c, "action_done")
	if resp["ok"] != false {
		t.Fatalf("action_done resp = %+v, want ok=false", resp)
	}
}

// --- git dispatch tests ---

func TestWSGitDispatch(t *testing.T) {
	m := newFakeMulti()
	m.addProject(t, "repo", "", newFakeBackend())
	srv := startStack(t, m)

	tests := []struct {
		msg      map[string]any
		wantType string
	}{
		{msg: map[string]any{"type": "git_log", "project": "repo"}, wantType: "git_commits"},
		{msg: map[string]any{"type": "git_diff", "project": "repo", "hash": "abc123"}, wantType: "git_diff"},
		{msg: map[string]any{"type": "git_commit", "project": "repo", "message": "msg"}, wantType: "git_commit_result"},
		{msg: map[string]any{"type": "git_stage", "project": "repo", "stage_all": true}, wantType: "git_stage_result"},
	}
	for _, tt := range tests {
		t.Run(tt.msg["type"].(string), func(t *testing.T) {
			c := dialWS(t, srv.Addr())
			sendWSMsg(t, c, tt.msg)
			resp := expectType(t, c, tt.wantType)
			if resp["ok"] != true {
				t.Fatalf("resp = %+v, want ok=true", resp)
			}
			if tt.wantType == "git_commits" {
				data, _ := json.Marshal(resp["data"])
				var payload struct {
					Commits []protocol.GitCommit `json:"commits"`
				}
				if err := json.Unmarshal(data, &payload); err != nil || len(payload.Commits) != 1 {
					t.Fatalf("payload = %s (%v)", data, err)
				}
			}
		})
	}
}

func TestWSGitRemoteOutput(t *testing.T) {
	m := newFakeMulti()
	m.addProject(t, "repo", "", newFakeBackend())
	srv := startStack(t, m)
	c := dialWS(t, srv.Addr())

	for _, tt := range []struct{ typeName, want string }{
		{"git_push", "pushed"},
		{"git_pull", "pulled"},
		{"git_fetch", "fetched"},
	} {
		sendWSMsg(t, c, map[string]string{"type": tt.typeName, "project": "repo"})
		resp := expectType(t, c, "git_remote_result")
		if resp["ok"] != true || resp["line"] != tt.want {
			t.Fatalf("%s resp = %+v, want line=%q", tt.typeName, resp, tt.want)
		}
	}
}

// --- log streaming (through the real daemon tailer) ---

func appendToLog(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestWSSubscribeLogsHistoryAndLive(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "web.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFakeMulti()
	b := newFakeBackend()
	b.logPaths["web"] = logPath
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "subscribe_logs", "project": "api", "service": "web"})

	for _, want := range []string{"line1", "line2"} {
		msg := expectType(t, c, "log_line")
		if msg["service"] != "web" || msg["prev"] == true {
			t.Fatalf("log_line routing/tag wrong: %+v", msg)
		}
		if msg["line"] != want {
			t.Fatalf("line = %v, want %q", msg["line"], want)
		}
	}

	// Live tailing: appending surfaces as another log_line.
	appendToLog(t, logPath, "line3\n")
	msg := expectType(t, c, "log_line")
	if msg["line"] != "line3" {
		t.Fatalf("line = %v, want line3", msg["line"])
	}

	// Unsubscribe stops the flow.
	sendWSMsg(t, c, map[string]any{"type": "unsubscribe_logs", "project": "api", "service": "web"})
	appendToLog(t, logPath, "line4\n")
	if extra, ok := recvWSMsgTimeout(t, c, 500*time.Millisecond); ok {
		t.Fatalf("received message after unsubscribe: %v", extra)
	}
}

func TestWSSubscribePreviousLogs(t *testing.T) {
	dir := t.TempDir()
	prevPath := filepath.Join(dir, "web.prev.log")
	if err := os.WriteFile(prevPath, []byte("old1\nold2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFakeMulti()
	b := newFakeBackend()
	b.logPaths["web"] = filepath.Join(dir, "web.log")
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "subscribe_logs", "project": "api", "service": "web", "prev": true})

	for i, want := range []string{"old1", "old2"} {
		msg := expectType(t, c, "log_line")
		if msg["prev"] != true {
			t.Fatalf("previous stream line not tagged prev: %+v", msg)
		}
		if msg["line"] != want {
			t.Fatalf("line[%d] = %v, want %q", i, msg["line"], want)
		}
	}
	// A previous run's log never follows; the stream ends after draining.
	if extra, ok := recvWSMsgTimeout(t, c, 400*time.Millisecond); ok {
		t.Fatalf("previous-log stream kept sending: %v", extra)
	}
}

func TestWSLogRotationMarkerForwarded(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "web.log")
	if err := os.WriteFile(logPath, []byte("gen1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFakeMulti()
	b := newFakeBackend()
	b.logPaths["web"] = logPath
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "subscribe_logs", "project": "api", "service": "web"})
	expectType(t, c, "log_line") // gen1

	// Simulate the supervisor rotating: swap in a fresh file under the same
	// path while the daemon holds the old handle open.
	if err := os.Rename(logPath, logPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("gen2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := expectType(t, c, "log_rotated")
	if msg["service"] != "web" {
		t.Fatalf("log_rotated = %+v", msg)
	}
	next := expectType(t, c, "log_line")
	if next["line"] != "gen2" {
		t.Fatalf("post-rotation line = %v, want gen2", next["line"])
	}
}

func TestWSSubscribeActionLogsWaitsForFirstRun(t *testing.T) {
	dir := t.TempDir()
	m := newFakeMulti()
	b := newFakeBackend()
	b.actions = []protocol.ActionInfo{{Name: "migrate", Command: "bin/migrate"}}
	m.addProject(t, "api", "", b)
	srv := startStack(t, m)

	actionLog := filepath.Join(dir, "migrate.log")
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := os.WriteFile(actionLog, []byte("migration done\n"), 0o644); err != nil {
			t.Error(err)
			return
		}
		b.setActionLogPath("migrate", actionLog)
	}()

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "subscribe_action_logs", "project": "api", "action": "migrate"})
	msg := expectType(t, c, "log_line")
	if msg["action"] != "migrate" || msg["line"] != "migration done" {
		t.Fatalf("action log line routed wrong: %+v", msg)
	}
}

// --- event fan-out ---

func TestWSEventFanOut(t *testing.T) {
	m := newFakeMulti()
	m.addProject(t, "api", "", newFakeBackend())
	srv := startStack(t, m)

	c1 := dialWS(t, srv.Addr())
	c2 := dialWS(t, srv.Addr())

	// Give both daemon-side event subscriptions a beat to register.
	time.Sleep(150 * time.Millisecond)

	state := protocol.ServiceState{Name: "web", Status: "exited", ExitCode: 1}
	m.fireState("api", state)
	for _, c := range []*websocket.Conn{c1, c2} {
		msg := expectType(t, c, "state_changed")
		if msg["project"] != "api" || msg["service"] != "web" {
			t.Fatalf("state_changed = %+v", msg)
		}
		data, _ := json.Marshal(msg["data"])
		var got protocol.ServiceState
		if err := json.Unmarshal(data, &got); err != nil || got.Status != "exited" {
			t.Fatalf("state payload = %s (%v)", data, err)
		}
	}

	m.fireActionState("api", protocol.ActionState{Name: "migrate", Status: "running"})
	for _, c := range []*websocket.Conn{c1, c2} {
		msg := expectType(t, c, "action_state_changed")
		if msg["action"] != "migrate" {
			t.Fatalf("action_state_changed = %+v", msg)
		}
	}

	m.fireGit("api")
	for _, c := range []*websocket.Conn{c1, c2} {
		msg := expectType(t, c, "git_changed")
		if msg["project"] != "api" {
			t.Fatalf("git_changed = %+v", msg)
		}
	}
}

// --- terminals ---

func TestWSSpawnTerminalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := newFakeMulti()
	m.addProject(t, "api", filepath.Join(dir, "local-compose.yml"), newFakeBackend())
	srv := startStack(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]any{"type": "spawn_terminal", "id": "t1", "project": "api", "cols": 80, "rows": 24})

	// Send echo immediately; don't depend on when (or whether) the shell
	// paints its first prompt.
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
			fmt.Fprintf(&blob, "%v", msg["output"]) //nolint:errcheck
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

	// A known-name miss must also be rejected rather than silently opening a
	// shell in the web process's cwd.
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
