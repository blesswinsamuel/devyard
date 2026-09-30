// Package engine owns the lifecycle of projects, services and tasks.
//
// Every service, task and project is an actor: one goroutine that owns all
// of its state and processes commands and events from a mailbox in order.
// Nothing else mutates that state, so concurrent API calls can never race
// each other into duplicate processes or stuck states. Commands reply once
// their effect is established (Stop replies when the process is gone).
//
// Processes are launched through runners (internal/runner), which survive the
// daemon; adopting an existing process after a daemon restart is the same
// code path as observing a freshly launched one.
package engine

import (
	"context"
	"errors"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// Errors returned by engine commands. The API layer maps them to status
// codes; callers must use errors.Is, never message matching.
var (
	ErrNotFound       = errors.New("not found")
	ErrAlreadyExists  = errors.New("already exists")
	ErrNotRunning     = errors.New("not running")
	ErrAlreadyRunning = errors.New("already running")
	ErrInvalid        = errors.New("invalid argument")
	ErrConfig         = errors.New("config error")
	ErrShuttingDown   = errors.New("daemon is shutting down")
)

// Service statuses.
const (
	StatusStopped  = "stopped"
	StatusWaiting  = "waiting"
	StatusBuilding = "building"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusBackoff  = "backoff"
	StatusExited   = "exited"
	StatusFailed   = "failed"
	// StatusIdle is used by tasks that have never run.
	StatusIdle = "idle"
)

// Health states.
const (
	HealthNone      = ""
	HealthStarting  = "starting"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// Project statuses and desired states.
const (
	ProjectStopped  = "stopped"
	ProjectStarting = "starting"
	ProjectRunning  = "running"
	ProjectDegraded = "degraded"
	ProjectStopping = "stopping"
	ProjectError    = "error"

	DesiredRunning = "running"
	DesiredStopped = "stopped"
	DesiredPartial = "partial"
)

// Dep is one depends_on edge.
type Dep struct {
	Name      string
	Condition config.DependsOnCondition
}

// ProcessDef is the resolved, immutable definition of a service or task.
type ProcessDef struct {
	Project string
	Kind    string // "service" | "task"
	Name    string

	Command string
	Shell   string
	Dir     string
	Env     []string
	EnvKeys []string
	TTY     bool
	Deps    []Dep

	// Service-only.
	Restart     config.RestartPolicy
	Health      *config.Healthcheck
	Build       *runner.BuildSpec
	Ports       []config.ServicePort
	ProxyHost   string
	StopGrace   time.Duration
	ProcDir     string
	Socket      string
	RuntimeHash string
}

// ServiceState is the published state of a service.
type ServiceState struct {
	Status        string
	Health        string
	HealthDetail  string
	PID           int
	ExitCode      int
	Restarts      int
	Run           int64
	StartedAt     time.Time
	FinishedAt    time.Time
	Message       string
	NextRestartAt time.Time
}

// Active reports whether a process exists or is about to (the service holds
// resources and must be stopped).
func (s ServiceState) Active() bool {
	switch s.Status {
	case StatusBuilding, StatusStarting, StatusRunning, StatusStopping:
		return true
	}
	return false
}

// TaskState is the published state of a task.
type TaskState struct {
	Status     string
	PID        int
	ExitCode   int
	Run        int64
	StartedAt  time.Time
	FinishedAt time.Time
	Message    string
	Args       []string
}

// ProjectState is the published state of a project.
type ProjectState struct {
	ID             string
	ConfigPath     string
	EnvFile        string
	Status         string
	Desired        string
	Error          string
	ServicesTotal  int
	ServicesActive int
	DefaultService string
	UpdatedAt      time.Time
}

// Observer receives every published state change. Calls for one entity are
// made from a single goroutine, in order. Implementations must not block for
// long and must not call back into the engine synchronously.
type Observer interface {
	ProjectChanged(ProjectState)
	ProjectRemoved(id string)
	ServiceChanged(def *ProcessDef, st ServiceState)
	ServiceRemoved(project, name string)
	TaskChanged(def *ProcessDef, st TaskState)
	TaskRemoved(project, name string)
}

// Proc is a handle on one run (see runner.Process).
type Proc interface {
	Watch(ctx context.Context, fn func(runner.Status)) (runner.Status, error)
	Stop(ctx context.Context, grace time.Duration) (runner.Status, error)
	Signal(ctx context.Context, sig string) error
	Attach(ctx context.Context, cols, rows int) (*runner.Attachment, error)
}

// Launcher starts and re-opens runs. The daemon uses runner.Launch/Open;
// tests use fakes.
type Launcher interface {
	Launch(ctx context.Context, spec runner.Spec) (Proc, error)
	// Open returns the recorded run of procDir. When the run is still
	// live, proc is non-nil.
	Open(procDir string) (proc Proc, st runner.Status, err error)
}

// RunnerLauncher launches real runner processes.
type RunnerLauncher struct {
	Options runner.LaunchOptions
}

// Launch implements Launcher.
func (l RunnerLauncher) Launch(ctx context.Context, spec runner.Spec) (Proc, error) {
	return runner.Launch(ctx, spec, l.Options)
}

// Open implements Launcher.
func (l RunnerLauncher) Open(procDir string) (Proc, runner.Status, error) {
	p, st, err := runner.Open(procDir)
	if err != nil {
		return nil, runner.Status{}, err
	}
	if st.Exited() {
		return nil, st, nil
	}
	if !runner.Alive(st.RunnerPID) {
		// The runner died without recording an exit (machine reboot or a
		// crash). Resolve it as lost; this also kills any orphaned group.
		final, err := p.Wait(context.Background())
		if err != nil {
			return nil, st, err
		}
		return nil, final, nil
	}
	return p, st, nil
}
