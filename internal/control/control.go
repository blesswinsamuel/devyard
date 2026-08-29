package control

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/gitlog"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// Backend is the surface the control server needs from the supervisor.
type Backend interface {
	States() []*protocol.ServiceState
	Stop(ctx context.Context) error
	StopService(name string) error
	StartService(name string) error
	KillService(name, signal string) error
	Restart(name string) error
	Top(name string) ([]*protocol.ServiceStat, error)
	ListActions() []*protocol.ActionState
	RunAction(ctx context.Context, name string, args []string, out io.Writer) (int, error)
	LogPath(name string) (string, error)
	PreviousLogPath(name string) (string, error)
	ActionLogPath(name string) (string, error)
	ActionPreviousLogPath(name string) (string, error)
	Ports() ([]*protocol.PortBinding, error)
}

// MultiBackend is the surface the control server needs from the daemon. It
// manages multiple projects and routes per-project requests to the right
// Backend.
type MultiBackend interface {
	ListProjects() []*protocol.ProjectInfo
	StartProject(configPath string, build bool, envFile string, removeOrphans bool) error
	StartService(project, service string) error
	StopProject(name string) error
	RemoveProject(name string) error
	StopDaemon() error
	DaemonStatus() (*protocol.DaemonInfo, error)
	RestartDaemon(restartServices bool) error
	ProjectBackend(project string) (Backend, error)
	ListServices(project string) ([]*protocol.ServiceState, error)
	ListActions(project string) ([]*protocol.ActionState, error)

	GitLog(project string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error)
	GitDiff(project string, hash string, path string, contextLines ...int) (*protocol.GitDiffResult, error)
	GitCommit(project string, message string) error
	GitStage(project string, path string, stageAll bool, unstage bool) error
	GitPush(project string) (string, error)
	GitPull(project string) (string, error)
	GitFetch(project string) (string, error)
	Ports(project string) ([]*protocol.PortBinding, error)

	SetOnStateChange(fn func(project string, state *protocol.ServiceState))
	SetOnActionStateChange(fn func(project string, state *protocol.ActionState))
	SetOnGitChange(fn func(project string))
}

// SingleProjectBackend adapts a single Backend to the MultiBackend interface.
// It is used by tests so the control server can use the same MultiBackend
// dispatch code. Project is the project name (may be empty for anonymous
// single-project servers).
type SingleProjectBackend struct {
	Backend
	Project string
	// GitDir is the working directory used for GitLog. Empty runs git in the
	// process's current working directory.
	GitDir string
}

func (s SingleProjectBackend) ListProjects() []*protocol.ProjectInfo {
	return []*protocol.ProjectInfo{{Name: s.Project, Status: "running"}}
}

func (s SingleProjectBackend) GitLog(string) ([]*protocol.GitCommit, []*protocol.GitBranch, []*protocol.GitTag, []*protocol.GitStash, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return nil, nil, nil, nil, fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Log(dir)
}

func (s SingleProjectBackend) GitDiff(_ string, hash string, path string, contextLines ...int) (*protocol.GitDiffResult, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return nil, fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Diff(dir, hash, path, contextLines...)
}

func (s SingleProjectBackend) GitCommit(_ string, message string) error {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Commit(dir, message)
}

func (s SingleProjectBackend) GitStage(_ string, path string, stageAll bool, unstage bool) error {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Stage(dir, path, stageAll, unstage)
}

func (s SingleProjectBackend) GitPush(_ string) (string, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return "", fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Push(dir, "")
}

func (s SingleProjectBackend) GitPull(_ string) (string, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return "", fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Pull(dir, "")
}

func (s SingleProjectBackend) GitFetch(_ string) (string, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return "", fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Fetch(dir, "")
}

func (s SingleProjectBackend) StartProject(configPath string, build bool, envFile string, removeOrphans bool) error {
	return fmt.Errorf("start_project not supported in single-project mode")
}

func (s SingleProjectBackend) StartService(project, service string) error {
	if project != "" && project != s.Project {
		return fmt.Errorf("unknown project %q", project)
	}
	return s.Backend.StartService(service)
}

func (s SingleProjectBackend) StopProject(name string) error {
	if name != "" && name != s.Project {
		return fmt.Errorf("unknown project %q", name)
	}
	return s.Stop(context.Background())
}

func (s SingleProjectBackend) RemoveProject(name string) error {
	if name != "" && name != s.Project {
		return fmt.Errorf("unknown project %q", name)
	}
	return s.Stop(context.Background())
}

func (s SingleProjectBackend) StopDaemon() error {
	return s.Stop(context.Background())
}

func (s SingleProjectBackend) DaemonStatus() (*protocol.DaemonInfo, error) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return &protocol.DaemonInfo{
		Pid:         int32(os.Getpid()),
		StartTime:   protocol.TimeToProto(time.Now()),
		Goroutines:  int32(runtime.NumGoroutine()),
		MemoryAlloc: mem.Alloc,
		MemorySys:   mem.Sys,
		GoVersion:   runtime.Version(),
	}, nil
}

func (s SingleProjectBackend) RestartDaemon(restartServices bool) error {
	return s.Stop(context.Background())
}

func (s SingleProjectBackend) ProjectBackend(project string) (Backend, error) {
	if project != "" && project != s.Project {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	return s.Backend, nil
}

func (s SingleProjectBackend) SetOnStateChange(func(string, *protocol.ServiceState)) {}

func (s SingleProjectBackend) SetOnActionStateChange(func(string, *protocol.ActionState)) {}

func (s SingleProjectBackend) SetOnGitChange(func(string)) {}

func (s SingleProjectBackend) ListServices(project string) ([]*protocol.ServiceState, error) {
	if project != "" && project != s.Project {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	states := s.States()
	for _, st := range states {
		st.Project = s.Project
	}
	return states, nil
}

func (s SingleProjectBackend) ListActions(project string) ([]*protocol.ActionState, error) {
	if project != "" && project != s.Project {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	actions := s.Backend.ListActions()
	for _, act := range actions {
		act.Project = s.Project
	}
	return actions, nil
}

func (s SingleProjectBackend) Ports(project string) ([]*protocol.PortBinding, error) {
	if project != "" && project != s.Project {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	return s.Backend.Ports()
}
