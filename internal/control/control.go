package control

import (
	"context"
	"fmt"
	"os"

	"github.com/blesswinsamuel/local-compose/internal/gitlog"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// MultiBackend is the surface the control server needs from the daemon. It
// manages multiple projects and routes per-project requests to the right
// Backend.
type MultiBackend interface {
	// ListProjects returns a snapshot of all known projects.
	ListProjects() []protocol.ProjectInfo
	// StartProject loads the config at configPath and starts a supervisor
	// for it. If build is true, pre-start builds are run first. envFile is
	// the absolute path to an env file (empty falls back to .env next to
	// the config file). removeOrphans controls whether services deleted from
	// the config are stopped on reload.
	StartProject(configPath string, build bool, envFile string, removeOrphans bool) error
	// StartService starts one service of a project by name. When the project
	// is running the service is resumed in place; when the project is stopped
	// a supervisor is lazily materialized that starts just the requested
	// service and its depends_on chain.
	StartService(project, service string) error
	// StopProject stops the named project's services.
	StopProject(name string) error
	// RemoveProject stops the named project's services and removes it from the daemon.
	RemoveProject(name string) error
	// StopDaemon stops all projects and signals the daemon to exit.
	StopDaemon() error
	// ProjectBackend returns the Backend for the named project, or an
	// error if the project is not running. An empty project name is valid
	// for single-project servers.
	ProjectBackend(project string) (Backend, error)
	// GitLog returns the git commit log for the named project's working
	// directory, newest first. It errors if the project's directory is not a
	// git repository.
	GitLog(project string) ([]protocol.GitCommit, error)
	// GitDiff returns the commit metadata, changed files list, and diff for hash
	// in the named project's working directory.
	GitDiff(project string, hash string) (*protocol.GitDiffResult, error)
	// GitCommit stages changes and creates a commit with message in the project's repository.
	GitCommit(project string, message string) error
	// SetOnStateChange registers a callback that is invoked whenever any
	// project's service state changes. The project name and the
	// per-service state snapshot are passed. Implementations that do not
	// support push notifications may leave this as a no-op.
	SetOnStateChange(fn func(project string, state protocol.ServiceState))
	// SetOnActionStateChange registers a callback that is invoked whenever any
	// project's action runtime state changes.
	SetOnActionStateChange(fn func(project string, state protocol.ActionState))
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

func (s SingleProjectBackend) ListProjects() []protocol.ProjectInfo {
	return []protocol.ProjectInfo{{Name: s.Project, Status: "running"}}
}

func (s SingleProjectBackend) GitLog(string) ([]protocol.GitCommit, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return nil, fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Log(dir)
}

func (s SingleProjectBackend) GitDiff(_ string, hash string) (*protocol.GitDiffResult, error) {
	dir := s.GitDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if !gitlog.IsRepo(dir) {
		return nil, fmt.Errorf("project %q is not a git repository", s.Project)
	}
	return gitlog.Diff(dir, hash)
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

func (s SingleProjectBackend) ProjectBackend(project string) (Backend, error) {
	if project != "" && project != s.Project {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	return s.Backend, nil
}

func (s SingleProjectBackend) SetOnStateChange(func(string, protocol.ServiceState)) {}

func (s SingleProjectBackend) SetOnActionStateChange(func(string, protocol.ActionState)) {}
