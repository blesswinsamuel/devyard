package control

import (
	"context"
	"fmt"

	"github.com/blesswinsamuel/local-compose/internal/protocol"
)

// MultiBackend is the surface the control server needs from the daemon. It
// manages multiple projects and routes per-project requests to the right
// Backend.
type MultiBackend interface {
	// ListProjects returns a snapshot of all known projects.
	ListProjects() []protocol.ProjectInfo
	// StartProject loads the config at configPath and starts a supervisor
	// for it. If build is true, pre-start builds are run first.
	StartProject(configPath string, build bool) error
	// StopProject stops the named project's services and removes it from
	// the daemon.
	StopProject(name string) error
	// StopDaemon stops all projects and signals the daemon to exit.
	StopDaemon() error
	// ProjectBackend returns the Backend for the named project, or an
	// error if the project is not running. An empty project name is valid
	// for single-project servers.
	ProjectBackend(project string) (Backend, error)
}

// SingleProjectBackend adapts a single Backend to the MultiBackend interface.
// It is used by tests so the control server can use the same MultiBackend
// dispatch code. Project is the project name (may be empty for anonymous
// single-project servers).
type SingleProjectBackend struct {
	Backend
	Project string
}

func (s SingleProjectBackend) ListProjects() []protocol.ProjectInfo {
	return []protocol.ProjectInfo{{Name: s.Project, Status: "running"}}
}

func (s SingleProjectBackend) StartProject(configPath string, build bool) error {
	return fmt.Errorf("start_project not supported in single-project mode")
}

func (s SingleProjectBackend) StopProject(name string) error {
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
