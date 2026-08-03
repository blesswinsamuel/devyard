package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/web"
)

// fakeMultiBackend is a minimal control.MultiBackend for testing the WS server.
type fakeMultiBackend struct {
	mu         sync.Mutex
	projects   map[string]control.Backend
	started    []string
	stopped    []string
	daemonStop chan struct{}
}

func newFakeMulti() *fakeMultiBackend {
	return &fakeMultiBackend{
		projects:   make(map[string]control.Backend),
		daemonStop: make(chan struct{}),
	}
}

func (m *fakeMultiBackend) ListProjects() []protocol.ProjectInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]protocol.ProjectInfo, 0, len(m.projects))
	for name, b := range m.projects {
		status := "running"
		if fb, ok := b.(*fakeBackend); ok {
			if fb.stoppedProj {
				status = "stopped"
			}
		}
		out = append(out, protocol.ProjectInfo{
			Name:   name,
			Status: status,
		})
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
	case <-m.daemonStop:
	default:
		close(m.daemonStop)
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

// fakeBackend is a minimal control.Backend for WS testing.
type fakeBackend struct {
	states      []protocol.ServiceState
	logPaths    map[string]string
	restarts    []string
	stopped     []string
	killed      []string
	stoppedProj bool
}

func (b *fakeBackend) States() []protocol.ServiceState { return b.states }
func (b *fakeBackend) Stop(context.Context) error      { b.stoppedProj = true; return nil }
func (b *fakeBackend) StopService(name string) error {
	b.stopped = append(b.stopped, name)
	return nil
}
func (b *fakeBackend) KillService(name, signal string) error {
	b.killed = append(b.killed, name)
	return nil
}
func (b *fakeBackend) Restart(name string) error {
	b.restarts = append(b.restarts, name)
	return nil
}
func (b *fakeBackend) Top(name string) ([]protocol.ServiceStat, error) {
	return nil, nil
}
func (b *fakeBackend) LogPath(name string) (string, error) {
	if p, ok := b.logPaths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("unknown service %q", name)
}

func newWebServer(t *testing.T, backend control.MultiBackend) *web.Server {
	t.Helper()
	srv := web.NewServer("127.0.0.1:0", backend)
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	url := fmt.Sprintf("ws://%s/ws", addr)
	c, _, err := websocket.Dial(context.Background(), url, nil)
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

func recvWSMsg(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return m
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

func TestWSListProjects(t *testing.T) {
	m := newFakeMulti()
	m.projects["api"] = &fakeBackend{states: []protocol.ServiceState{{Name: "svc", Status: "running"}}}
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "list_projects"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "projects" {
		t.Fatalf("type = %v, want projects", resp["type"])
	}
}

func TestWSListServices(t *testing.T) {
	m := newFakeMulti()
	m.projects["api"] = &fakeBackend{
		states: []protocol.ServiceState{{Name: "svc", Status: "running", PID: 42}},
	}
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "list_services", "project": "api"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "services" {
		t.Fatalf("type = %v, want services", resp["type"])
	}
	if resp["project"] != "api" {
		t.Errorf("project = %v, want api", resp["project"])
	}
}

func TestWSStartProject(t *testing.T) {
	m := newFakeMulti()
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "start_project", "config_path": "/path/to/local-compose.yml"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "result" || resp["ok"] != true {
		t.Fatalf("resp = %+v, want result ok=true", resp)
	}
	if len(m.started) != 1 || m.started[0] != "/path/to/local-compose.yml" {
		t.Errorf("started = %v", m.started)
	}
}

func TestWSStopProject(t *testing.T) {
	m := newFakeMulti()
	m.projects["api"] = &fakeBackend{}
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "stop_project", "project": "api"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "result" || resp["ok"] != true {
		t.Fatalf("resp = %+v, want result ok=true", resp)
	}
	if len(m.stopped) != 1 || m.stopped[0] != "api" {
		t.Errorf("stopped = %v", m.stopped)
	}
}

func TestWSRestartService(t *testing.T) {
	b := &fakeBackend{states: []protocol.ServiceState{{Name: "web", Status: "running"}}}
	m := newFakeMulti()
	m.projects["api"] = b
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "restart_service", "project": "api", "service": "web"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "result" || resp["ok"] != true {
		t.Fatalf("resp = %+v, want result ok=true", resp)
	}
	if len(b.restarts) != 1 || b.restarts[0] != "web" {
		t.Errorf("restarts = %v", b.restarts)
	}
}

func TestWSStopService(t *testing.T) {
	b := &fakeBackend{states: []protocol.ServiceState{{Name: "web", Status: "running"}}}
	m := newFakeMulti()
	m.projects["api"] = b
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "stop_service", "project": "api", "service": "web"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "result" || resp["ok"] != true {
		t.Fatalf("resp = %+v, want result ok=true", resp)
	}
	if len(b.stopped) != 1 || b.stopped[0] != "web" {
		t.Errorf("stopped = %v", b.stopped)
	}
}

func TestWSKillService(t *testing.T) {
	b := &fakeBackend{states: []protocol.ServiceState{{Name: "web", Status: "running"}}}
	m := newFakeMulti()
	m.projects["api"] = b
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "kill_service", "project": "api", "service": "web"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "result" || resp["ok"] != true {
		t.Fatalf("resp = %+v, want result ok=true", resp)
	}
	if len(b.killed) != 1 || b.killed[0] != "web" {
		t.Errorf("killed = %v", b.killed)
	}
}

func TestWSUnknownType(t *testing.T) {
	m := newFakeMulti()
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "bogus"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "error" {
		t.Fatalf("type = %v, want error", resp["type"])
	}
}

func TestWSListServicesUnknownProject(t *testing.T) {
	m := newFakeMulti()
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "list_services", "project": "nope"})
	resp := recvWSMsg(t, c)
	if resp["type"] != "error" {
		t.Fatalf("type = %v, want error", resp["type"])
	}
}

func TestWSSubscribeLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "web.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{logPaths: map[string]string{"web": logPath}}
	m := newFakeMulti()
	m.projects["api"] = b
	srv := newWebServer(t, m)

	c := dialWS(t, srv.Addr())
	sendWSMsg(t, c, map[string]string{"type": "subscribe_logs", "project": "api", "service": "web"})

	// Should receive existing log lines.
	resp := recvWSMsg(t, c)
	if resp["type"] != "log_line" || resp["line"] != "line1" {
		t.Fatalf("first line = %+v, want log_line line1", resp)
	}
	resp = recvWSMsg(t, c)
	if resp["type"] != "log_line" || resp["line"] != "line2" {
		t.Fatalf("second line = %+v, want log_line line2", resp)
	}

	// Append more content; the follow loop should pick it up.
	if err := appendToLog(logPath, "line3\n"); err != nil {
		t.Fatal(err)
	}
	resp, ok := recvWSMsgTimeout(t, c, 2*time.Second)
	if !ok {
		t.Fatalf("did not receive line3")
	}
	if resp["type"] != "log_line" || resp["line"] != "line3" {
		t.Fatalf("third line = %+v, want log_line line3", resp)
	}

	// Unsubscribe.
	sendWSMsg(t, c, map[string]string{"type": "unsubscribe_logs", "project": "api", "service": "web"})
	time.Sleep(200 * time.Millisecond)

	// Append more content; should NOT receive it after unsubscribe.
	_ = appendToLog(logPath, "line4\n")
	_, ok = recvWSMsgTimeout(t, c, 500*time.Millisecond)
	if ok {
		t.Fatalf("received message after unsubscribe")
	}
}

func TestHTTPServesIndex(t *testing.T) {
	m := newFakeMulti()
	srv := newWebServer(t, m)

	resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func appendToLog(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}

// Ensure fakeMultiBackend satisfies control.MultiBackend.
var _ control.MultiBackend = (*fakeMultiBackend)(nil)

// Ensure fakeBackend satisfies control.Backend.
var _ control.Backend = (*fakeBackend)(nil)

// Suppress unused warning for net import used by type assertion.
var _ = net.Listen
