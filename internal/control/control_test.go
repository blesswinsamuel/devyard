package control_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// fakeBackend is an in-memory control.Backend for exercising the server
// without spinning up real child processes.
type fakeBackend struct {
	mu           sync.Mutex
	states       []*protocol.ServiceState
	stopErr      error
	logPaths     map[string]string
	restarts     []string
	stopped      bool
	stoppedSvc   []string
	startedSvc   []string
	killedSvc    []string
	killedSigs   []string
	stopSvcErr   error
	startSvcErr  error
	tasks        []*protocol.TaskState
	runTaskFn    func(name string, args []string, out io.Writer) (int, error)
	stoppedTasks []string
	stopTaskErr  error
}

func (b *fakeBackend) ListTasks() []*protocol.TaskState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.TaskState, len(b.tasks))
	copy(out, b.tasks)
	return out
}

func (b *fakeBackend) RunTask(ctx context.Context, name string, args []string, out io.Writer) (int, error) {
	b.mu.Lock()
	fn := b.runTaskFn
	b.mu.Unlock()
	if fn != nil {
		return fn(name, args, out)
	}
	return 0, nil
}

func (b *fakeBackend) StopTask(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stoppedTasks = append(b.stoppedTasks, name)
	return b.stopTaskErr
}

func (b *fakeBackend) States() []*protocol.ServiceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.ServiceState, len(b.states))
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

func (b *fakeBackend) StartService(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startedSvc = append(b.startedSvc, name)
	return b.startSvcErr
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

func (b *fakeBackend) PreviousLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return strings.TrimSuffix(p, ".log") + ".prev.log", nil
	}
	return "", errors.New("unknown service " + name)
}

func (b *fakeBackend) TaskLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return p, nil
	}
	return "", errors.New("unknown task " + name)
}

func (b *fakeBackend) TaskPreviousLogPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.logPaths[name]; ok {
		return strings.TrimSuffix(p, ".log") + ".prev.log", nil
	}
	return "", errors.New("unknown task " + name)
}

func (b *fakeBackend) Top(name string) ([]*protocol.ServiceStat, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.ServiceStat, 0, len(b.states))
	for _, st := range b.states {
		if name != "" && st.Name != name {
			continue
		}
		out = append(out, &protocol.ServiceStat{
			Name:     st.Name,
			Status:   st.Status,
			Pid:      st.Pid,
			Pgid:     st.Pid,
			Procs:    1,
			Cpu:      12.5,
			RssBytes: 4096,
		})
	}
	return out, nil
}

func (b *fakeBackend) ListPorts() ([]*protocol.PortBinding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return []*protocol.PortBinding{
		{Project: "proj", Service: "web", Pid: 100, Ip: "127.0.0.1", Port: 3000, Protocol: "tcp"},
	}, nil
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
		states: []*protocol.ServiceState{
			{Name: "api", Status: "running", Pid: 123, HasHealth: true, Health: "healthy"},
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
	if got[0].Name != "api" || got[0].Pid != 123 || got[0].Health != "healthy" {
		t.Errorf("api state: %+v", got[0])
	}
	if got[1].Name != "web" || got[1].Status != "exited" {
		t.Errorf("web state: %+v", got[1])
	}
}

func TestRoundtripTasks(t *testing.T) {
	b := &fakeBackend{
		tasks: []*protocol.TaskState{
			{Name: "migrate", Command: "npx prisma db push", Status: "idle"},
		},
		runTaskFn: func(name string, args []string, out io.Writer) (int, error) {
			_, _ = fmt.Fprintln(out, "running migration...")
			return 0, nil
		},
	}
	srv := newServer(t, b)
	c1, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c1.Close() }()

	tasks, err := c1.ListTasks("")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "migrate" {
		t.Fatalf("ListTasks got %+v", tasks)
	}

	c2, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial 2: %v", err)
	}
	defer func() { _ = c2.Close() }()

	var output []string
	code, err := c2.RunTask(context.Background(), "", "migrate", nil, func(line string) {
		output = append(output, line)
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(output) != 1 || output[0] != "running migration..." {
		t.Errorf("output = %+v, want ['running migration...']", output)
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
	if err := c.StopProject(""); err != nil {
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

func TestRoundtripStopTask(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.StopTask("", "migrate"); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	if len(b.stoppedTasks) != 1 || b.stoppedTasks[0] != "migrate" {
		t.Errorf("stopped tasks = %+v, want [migrate]", b.stoppedTasks)
	}
}

func TestStopTaskRequiresName(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.StopTask("", ""); err == nil {
		t.Fatalf("StopTask with empty task name: expected error, got nil")
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
	if err := c.StopService("", ""); err == nil {
		t.Fatalf("StopService with empty name: expected error, got nil")
	}
}

func TestRoundtripStartService(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.StartService("", "api"); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	if len(b.startedSvc) != 1 || b.startedSvc[0] != "api" {
		t.Errorf("started = %v, want [api]", b.startedSvc)
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
	if err := c.KillService("", "api", "SIGINT"); err != nil {
		t.Fatalf("KillService: %v", err)
	}
	if b.killedCount("api") != 1 {
		t.Errorf("api kills = %d, want 1", b.killedCount("api"))
	}
}

func TestRoundtripRestartOne(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Restart("", "api"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if b.restartsFor("api") != 1 {
		t.Errorf("api restarts = %d, want 1", b.restartsFor("api"))
	}
}

func TestRoundtripTop(t *testing.T) {
	b := &fakeBackend{
		states: []*protocol.ServiceState{
			{Name: "api", Status: "running", Pid: 123},
			{Name: "web", Status: "running", Pid: 456},
		},
	}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	stats, err := c.Top("", "")
	if err != nil {
		t.Fatalf("Top: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2: %+v", len(stats), stats)
	}
	if stats[0].Name != "api" || stats[0].Pid != 123 || stats[0].Cpu != 12.5 {
		t.Errorf("api stat: %+v", stats[0])
	}
}

func TestRoundtripListPorts(t *testing.T) {
	b := &fakeBackend{}
	srv := newServer(t, b)
	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	ports, err := c.ListPorts("")
	if err != nil {
		t.Fatalf("ListPorts: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(ports))
	}
	if ports[0].Port != 3000 || ports[0].Service != "web" {
		t.Errorf("unexpected port: %+v", ports[0])
	}
}

func TestRoundtripLogsTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "api.log")
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
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
	if err := c.Logs("", "api", false, false, 2, func(l string) {
		lines = append(lines, l)
	}); err != nil {
		t.Fatalf("Logs: %v", err)
	}

	want := []string{"line 4", "line 5"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestRoundtripLogsFollow(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "api.log")
	if err := os.WriteFile(logPath, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{logPaths: map[string]string{"api": logPath}}
	srv := newServer(t, b)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	linesCh := make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = c.LogsCtx(ctx, "", "api", true, false, 0, func(l string) {
			linesCh <- l
		})
	}()

	if l := recvLine(t, linesCh, 2*time.Second); l != "initial" {
		t.Fatalf("first line = %q, want 'initial'", l)
	}

	_ = appendLog(logPath, "second\n")
	if l := recvLine(t, linesCh, 2*time.Second); l != "second" {
		t.Fatalf("second line = %q, want 'second'", l)
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

	if err := c.Logs("", "missing", false, false, 0, nil); err == nil {
		t.Errorf("Logs on missing service: expected error, got nil")
	}
}

func TestLogsPreviousRun(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "api.log")
	prevPath := filepath.Join(dir, "api.prev.log")
	if err := os.WriteFile(logPath, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prevPath, []byte("prev 1\nprev 2\n"), 0o644); err != nil {
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
	if err := c.Logs("", "api", false, true, 0, func(l string) {
		lines = append(lines, l)
	}); err != nil {
		t.Fatalf("Logs previous: %v", err)
	}

	if len(lines) != 2 || lines[0] != "prev 1" || lines[1] != "prev 2" {
		t.Errorf("lines = %v, want ['prev 1', 'prev 2']", lines)
	}
}

func TestDialMissingSocket(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.sock")
	c, err := control.Dial(missing)
	if err == nil {
		// Dialing returns client, but first RPC fails
		_, err = c.List("")
		if err == nil {
			t.Errorf("Expected error on missing socket, got nil")
		}
	}
}

// --- multi-backend tests ---

type fakeMultiBackend struct {
	mu           sync.Mutex
	projects     map[string]control.Backend
	started      []string
	envFiles     []string
	stopped      []string
	startedSvc   []string
	onProjects   func()
	daemonStopCh chan struct{}
}

func newFakeMultiBackend() *fakeMultiBackend {
	return &fakeMultiBackend{
		projects:     make(map[string]control.Backend),
		daemonStopCh: make(chan struct{}),
	}
}

func (m *fakeMultiBackend) ListProjects() []*protocol.ProjectInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*protocol.ProjectInfo, 0, len(m.projects))
	for name, b := range m.projects {
		status := "running"
		if fb, ok := b.(*fakeBackend); ok {
			fb.mu.Lock()
			if fb.stopped {
				status = "stopped"
			}
			fb.mu.Unlock()
		}
		out = append(out, &protocol.ProjectInfo{Name: name, Status: status})
	}
	return out
}

func (m *fakeMultiBackend) StartProject(configPath string, build bool, envFile string, removeOrphans bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = append(m.started, configPath)
	m.envFiles = append(m.envFiles, envFile)
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

func (m *fakeMultiBackend) StartService(project, service string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.projects[project]
	if !ok {
		return fmt.Errorf("project %q not running", project)
	}
	m.startedSvc = append(m.startedSvc, project+"/"+service)
	return b.StartService(service)
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

func (m *fakeMultiBackend) DaemonStatus() (*protocol.DaemonInfo, error) {
	return &protocol.DaemonInfo{
		Pid:         1234,
		StartTime:   protocol.TimeToProto(time.Now()),
		Goroutines:  10,
		MemoryAlloc: 1024,
		MemorySys:   2048,
		MemoryRss:   4096,
		GoVersion:   "go1.26.0",
	}, nil
}

func (m *fakeMultiBackend) RestartDaemon(restartServices bool) (int32, error) {
	return 0, m.StopDaemon()
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

func (m *fakeMultiBackend) SetOnStateChange(func(string, *protocol.ServiceState))  {}
func (m *fakeMultiBackend) SetOnTaskStateChange(func(string, *protocol.TaskState)) {}
func (m *fakeMultiBackend) SetOnGitChange(func(string))                            {}
func (m *fakeMultiBackend) SetOnProjectsChange(fn func()) {
	m.mu.Lock()
	m.onProjects = fn
	m.mu.Unlock()
}

func (m *fakeMultiBackend) GitLog(string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	return nil, nil, nil, nil, nil
}
func (m *fakeMultiBackend) GitDiff(string, string, string, ...int) (*protocol.GitDiffResult, error) {
	return nil, nil
}
func (m *fakeMultiBackend) GitCommit(string, string) error            { return nil }
func (m *fakeMultiBackend) GitStage(string, string, bool, bool) error { return nil }
func (m *fakeMultiBackend) GitPush(string) (string, error)            { return "pushed", nil }
func (m *fakeMultiBackend) GitPull(string) (string, error)            { return "pulled", nil }
func (m *fakeMultiBackend) GitFetch(string) (string, error)           { return "fetched", nil }
func (m *fakeMultiBackend) GitStatus(project string) (*protocol.GitStatus, error) {
	return &protocol.GitStatus{
		Project: project,
		Branch:  "main",
		IsRepo:  true,
		IsClean: true,
	}, nil
}
func (m *fakeMultiBackend) ListServices(project string) ([]*protocol.ServiceState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if project != "" {
		b, ok := m.projects[project]
		if !ok {
			return nil, fmt.Errorf("project %q not running", project)
		}
		states := b.States()
		for _, st := range states {
			st.Project = project
		}
		return states, nil
	}
	var all []*protocol.ServiceState
	for name, b := range m.projects {
		states := b.States()
		for _, st := range states {
			st.Project = name
		}
		all = append(all, states...)
	}
	return all, nil
}

func (m *fakeMultiBackend) ListTasks(project string) ([]*protocol.TaskState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if project != "" {
		b, ok := m.projects[project]
		if !ok {
			return nil, fmt.Errorf("project %q not running", project)
		}
		tasks := b.ListTasks()
		for _, act := range tasks {
			act.Project = project
		}
		return tasks, nil
	}
	var all []*protocol.TaskState
	for name, b := range m.projects {
		tasks := b.ListTasks()
		for _, act := range tasks {
			act.Project = name
		}
		all = append(all, tasks...)
	}
	return all, nil
}

func (m *fakeMultiBackend) ListPorts(string) ([]*protocol.PortBinding, error) { return nil, nil }

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

	if err := c.StartProject("/path/to/local-compose.yml", false, "/path/to/.env", true); err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	if len(m.started) != 1 || m.started[0] != "/path/to/local-compose.yml" {
		t.Errorf("started = %v, want [/path/to/local-compose.yml]", m.started)
	}
	if len(m.envFiles) != 1 || m.envFiles[0] != "/path/to/.env" {
		t.Errorf("envFiles = %v, want [/path/to/.env]", m.envFiles)
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
		states: []*protocol.ServiceState{{Name: "svc", Status: "running", Pid: 42}},
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
	if len(got) != 1 || got[0].Name != "svc" || got[0].Pid != 42 {
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

func TestDaemonStatusAndRestart(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c1, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c1.Close() }()

	info, err := c1.DaemonStatus()
	if err != nil {
		t.Fatalf("DaemonStatus: %v", err)
	}
	if info.Pid != 1234 || info.GoVersion == "" {
		t.Errorf("DaemonStatus = %+v, want PID=1234 and non-empty GoVersion", info)
	}

	c2, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c2.Close() }()

	if _, err := c2.RestartDaemon(false); err != nil {
		t.Fatalf("RestartDaemon: %v", err)
	}
	select {
	case <-m.daemonStopCh:
	case <-time.After(time.Second):
		t.Fatal("RestartDaemon did not trigger daemonStopCh")
	}
}

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

func TestSubscribeEventsHeartbeatAndProjectsChanged(t *testing.T) {
	m := newFakeMultiBackend()
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events := make(chan *protocol.Event, 10)
	go func() {
		_ = c.SubscribeEvents(ctx, func(ev *protocol.Event) {
			events <- ev
		})
	}()

	// First event must be the immediate heartbeat (flushes HTTP headers)
	select {
	case ev := <-events:
		if ev.GetHeartbeat() == nil {
			t.Fatalf("expected initial HeartbeatEvent, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial heartbeat")
	}

	// Trigger projects change callback
	m.mu.Lock()
	fn := m.onProjects
	m.mu.Unlock()
	if fn != nil {
		fn()
	}

	// Second event must be ProjectsChanged
	select {
	case ev := <-events:
		if ev.GetProjectsChanged() == nil {
			t.Fatalf("expected ProjectsChangedEvent, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for projects_changed event")
	}
}

func TestGitSyncEventBroadcast(t *testing.T) {
	m := newFakeMultiBackend()
	m.projects["api"] = &fakeBackend{}
	srv := newMultiServer(t, m)

	c, err := control.Dial(srv.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events := make(chan *protocol.Event, 10)
	go func() {
		_ = c.SubscribeEvents(ctx, func(ev *protocol.Event) {
			events <- ev
		})
	}()

	// First event must be the immediate heartbeat (flushes HTTP headers)
	select {
	case ev := <-events:
		if ev.GetHeartbeat() == nil {
			t.Fatalf("expected initial HeartbeatEvent, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial heartbeat")
	}

	if _, err := c.GitPush("api"); err != nil {
		t.Fatalf("GitPush: %v", err)
	}

	// Next events must be git_sync running=true followed by running=false.
	for _, wantRunning := range []bool{true, false} {
		select {
		case ev := <-events:
			sync := ev.GetGitSync()
			if sync == nil {
				t.Fatalf("expected GitSyncEvent, got %+v", ev)
			}
			if sync.Project != "api" || sync.Operation != "push" || sync.Running != wantRunning {
				t.Fatalf("GitSyncEvent = %+v, want project=api operation=push running=%v", sync, wantRunning)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for git_sync event (running=%v)", wantRunning)
		}
	}
}
