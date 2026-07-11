package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// fakeBackend is a minimal control.Backend for exercising the TUI's action and
// list commands without spinning up real child processes.
type fakeBackend struct {
	states     []protocol.ServiceState
	restarts   []string
	stopped    []string
	downCalled bool
	logPaths   map[string]string
}

func (b *fakeBackend) States() []protocol.ServiceState { return b.states }
func (b *fakeBackend) Stop(context.Context) error      { b.downCalled = true; return nil }
func (b *fakeBackend) StopService(name string) error {
	b.stopped = append(b.stopped, name)
	return nil
}
func (b *fakeBackend) Restart(name string) error { b.restarts = append(b.restarts, name); return nil }
func (b *fakeBackend) LogPath(name string) (string, error) {
	if p, ok := b.logPaths[name]; ok {
		return p, nil
	}
	return "", errors.New("unknown service " + name)
}

// newTestServer starts a control.Server backed by b on a short socket and
// returns it, cleaning up on test end.
func newTestServer(t *testing.T, b *fakeBackend) *control.Server {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lc-tui")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	srv := control.NewServer(filepath.Join(dir, "s.sock"), control.SingleProjectBackend{Backend: b, Project: "test"})
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// newTestModel returns a model wired to socket with a usable viewport, in the
// services view for project "test".
func newTestModel(t *testing.T, socket string) model {
	t.Helper()
	m := model{
		socket:      socket,
		project:     "test",
		currentView: viewServices,
		width:       80,
		height:      24,
		ready:       true,
		pane:        paneList,
	}
	m.layoutViewport()
	return m
}

func states(names ...string) []protocol.ServiceState {
	out := make([]protocol.ServiceState, len(names))
	for i, n := range names {
		out[i] = protocol.ServiceState{Name: n, Status: "running", PID: 1000 + i}
	}
	return out
}

func TestHandleStatesClampsSelection(t *testing.T) {
	m := newTestModel(t, "")
	m.selected = 5
	out, _ := m.Update(statesMsg{states: states("a", "b", "c")})
	mm := out.(model)
	if mm.selected != 2 {
		t.Fatalf("selected = %d, want 2", mm.selected)
	}
	if mm.selectedName() != "c" {
		t.Fatalf("selectedName = %q, want %q", mm.selectedName(), "c")
	}
}

func TestHandleProjectsDialFailureOffersStart(t *testing.T) {
	m := newTestModel(t, "")
	m.currentView = viewProjects
	out, _ := m.Update(projectsMsg{err: errors.New("dial: no such file")})
	mm := out.(model)
	if !mm.noDaemon {
		t.Fatalf("expected noDaemon=true on dial failure")
	}
	if !strings.Contains(mm.renderStartPrompt(), "No daemon running") {
		t.Fatalf("start prompt should mention daemon; got:\n%s", mm.renderStartPrompt())
	}
}

func TestListSelectionKeys(t *testing.T) {
	m := newTestModel(t, "")
	m.states = states("a", "b", "c")
	m.layoutViewport()

	out, _ := m.handleListKey(keyPress("down"))
	if out.(model).selected != 1 {
		t.Fatalf("after down: selected = %d, want 1", out.(model).selected)
	}

	out, _ = out.(model).handleListKey(keyPress("down"))
	if out.(model).selected != 2 {
		t.Fatalf("after second down: selected = %d, want 2", out.(model).selected)
	}

	out, _ = out.(model).handleListKey(keyPress("down")) // clamp at bottom
	if out.(model).selected != 2 {
		t.Fatalf("selected clamped at bottom: got %d, want 2", out.(model).selected)
	}

	out, _ = out.(model).handleListKey(keyPress("up"))
	if out.(model).selected != 1 {
		t.Fatalf("after up: selected = %d, want 1", out.(model).selected)
	}

	out, _ = out.(model).handleListKey(keyPress("g"))
	if out.(model).selected != 0 {
		t.Fatalf("after g: selected = %d, want 0", out.(model).selected)
	}
}

func TestQuitKeyTearsDown(t *testing.T) {
	m := newTestModel(t, "")
	m.states = states("a")
	out, cmd := m.handleKey(keyPress("q"))
	mm := out.(model)
	if !mm.quitting {
		t.Fatalf("expected quitting=true")
	}
	if cmd == nil {
		t.Fatalf("expected a quit command")
	}
	msg := runCmd(t, cmd)
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("quit cmd msg = %T, want tea.QuitMsg", msg)
	}
}

func TestRestartActionRoutesToBackend(t *testing.T) {
	b := &fakeBackend{states: states("api", "web")}
	srv := newTestServer(t, b)
	m := newTestModel(t, srv.Addr())
	m.states = b.states
	m.selected = 1 // web

	out, cmd := m.handleKey(keyPress("r"))
	if cmd == nil {
		t.Fatalf("expected a restart command")
	}
	msg := runCmd(t, cmd)
	r, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("action cmd msg = %T, want actionResultMsg", msg)
	}
	if r.action != "restart" || r.service != "web" || r.err != nil {
		t.Fatalf("restart result = %+v, want action=restart service=web err=nil", r)
	}
	if len(b.restarts) != 1 || b.restarts[0] != "web" {
		t.Fatalf("backend.restarts = %v, want [web]", b.restarts)
	}
	_ = out
}

func TestStopActionRoutesToBackend(t *testing.T) {
	b := &fakeBackend{states: states("api")}
	srv := newTestServer(t, b)
	m := newTestModel(t, srv.Addr())
	m.states = b.states

	_, cmd := m.handleKey(keyPress("s"))
	msg := runCmd(t, cmd)
	r, ok := msg.(actionResultMsg)
	if !ok || r.action != "stop" || r.service != "api" || r.err != nil {
		t.Fatalf("stop result = %+v, want action=stop service=api err=nil", r)
	}
	if len(b.stopped) != 1 || b.stopped[0] != "api" {
		t.Fatalf("backend.stopped = %v, want [api]", b.stopped)
	}
}

func TestDownActionRoutesToBackend(t *testing.T) {
	b := &fakeBackend{states: states("api")}
	srv := newTestServer(t, b)
	m := newTestModel(t, srv.Addr())
	m.states = b.states

	_, cmd := m.handleKey(keyPress("d"))
	msg := runCmd(t, cmd)
	r, ok := msg.(actionResultMsg)
	if !ok || r.action != "down" || r.err != nil {
		t.Fatalf("down result = %+v, want action=down err=nil", r)
	}
}

func TestBoundLogLines(t *testing.T) {
	lines := make([]string, 0, 16)
	for i := 0; i < maxLogLineCount+50; i++ {
		lines = boundLogLines(lines, "line", maxLogLineCount)
	}
	if len(lines) != maxLogLineCount {
		t.Fatalf("logLines = %d, want %d", len(lines), maxLogLineCount)
	}
	if lines[0] != "line" || lines[len(lines)-1] != "line" {
		t.Fatalf("logLines content unexpected: first=%q last=%q", lines[0], lines[len(lines)-1])
	}

	small := boundLogLines([]string{"a", "b", "c", "d"}, "e", 3)
	if len(small) != 3 || small[0] != "c" || small[2] != "e" {
		t.Fatalf("small cap = %v, want [c d e]", small)
	}
}

func TestRenderSplitContainsPanes(t *testing.T) {
	m := newTestModel(t, "")
	m.states = states("api", "web")
	m.selected = 0
	got := m.renderSplit()
	for _, want := range []string{"Services", "Logs: api", "api"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderSplit missing %q; got:\n%s", want, got)
		}
	}
}

func TestProjectListView(t *testing.T) {
	m := newTestModel(t, "")
	m.currentView = viewProjects
	m.projects = []protocol.ProjectInfo{
		{Name: "api", Status: "running"},
		{Name: "web", Status: "stopped"},
	}
	m.selectedProj = 0
	got := m.renderProjectsView()
	for _, want := range []string{"Projects", "api", "running", "web", "stopped"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderProjectsView missing %q; got:\n%s", want, got)
		}
	}
}

func TestEscBackToProjects(t *testing.T) {
	m := newTestModel(t, "")
	m.currentView = viewServices
	m.states = states("a")
	out, _ := m.handleServiceViewKey(keyPress("esc"))
	mm := out.(model)
	if mm.currentView != viewProjects {
		t.Fatalf("expected currentView=viewProjects, got %d", mm.currentView)
	}
}

// --- helpers ---

func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatalf("cmd is nil")
	}
	msg := cmd()
	if msg == nil {
		t.Fatalf("cmd returned nil msg")
	}
	return msg
}
