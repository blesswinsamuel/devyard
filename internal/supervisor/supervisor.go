// Package supervisor manages child processes, logging, and restart policy.
//
// The Supervisor spawns one process per service (in topological start order),
// each in its own process group so it can be signalled as a unit. stdout and
// stderr are captured to per-service log files and, in foreground mode, echoed
// to the terminal with a colored service-name prefix. One goroutine per
// service waits for exit and applies the configured restart policy with
// exponential backoff. A top-level signal handler turns SIGINT/SIGTERM into a
// graceful shutdown that signals every group.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// Status is the lifecycle state of a supervised service.
type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusBackoff  Status = "backoff"
	StatusExited   Status = "exited"
	StatusStopped  Status = "stopped" // explicitly stopped; not restarted
)

// DefaultGracefulStopTimeout is how long Stop waits for SIGTERM before SIGKILL.
const DefaultGracefulStopTimeout = 10 * time.Second

// Options configures a Supervisor.
type Options struct {
	// Locations are the resolved runtime/state dirs for the project. The
	// logs dir is where per-service log files are appended.
	Locations *project.Locations

	// File is the parsed config. Services are read from here.
	File *config.File

	// Order is the topological start order (dependencies first). It must
	// contain exactly the names in File.Services.
	Order []string

	// BaseDir is the directory the config file lives in. Service
	// working_dir values are resolved against it when relative.
	BaseDir string

	// Foreground, when true, mirrors each service's output to Stdout with a
	// colored service-name prefix. Daemon supervisors set this to false.
	Foreground bool

	// Stdout is where foreground lines are written. Defaults to os.Stdout.
	Stdout io.Writer

	// GracefulStopTimeout is the SIGTERM grace period before SIGKILL. If
	// zero, DefaultGracefulStopTimeout is used.
	GracefulStopTimeout time.Duration

	// Backoff tunes restart backoff. Zero values are replaced by
	// DefaultBackoff() in New.
	Backoff BackoffConfig

	// InstallSignalHandler, when true, makes Start install a SIGINT/SIGTERM
	// handler that triggers a graceful Stop. The daemon and foreground CLI
	// enable this; tests leave it off.
	InstallSignalHandler bool
}

// ServiceState is a point-in-time snapshot of a service used by ps/tui/web.
type ServiceState struct {
	Name       string
	Status     Status
	PID        int
	ExitCode   int
	Restarts   int
	StartedAt  time.Time
	FinishedAt time.Time
	HasHealth  bool
	Health     string // "n/a" until Phase 2 wires internal/health
}

// serviceRuntime is the supervisor's mutable per-service state.
type serviceRuntime struct {
	spec   config.Service
	name   string
	logger *serviceLogger

	mu         sync.Mutex
	status     Status
	pid        int
	pgid       int
	exitCode   int
	restarts   int
	startedAt  time.Time
	finishedAt time.Time

	// stopped is set when the service has been explicitly stopped (via Stop
	// or StopService) so the run loop does not restart it.
	stopped atomic.Bool

	// done is closed when the service's runService goroutine exits.
	done chan struct{}
}

// Supervisor owns and supervises a set of services.
type Supervisor struct {
	opts Options

	mu       sync.Mutex
	services map[string]*serviceRuntime
	order    []string

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	started atomic.Bool
	stopped atomic.Bool
}

// New constructs a Supervisor. It does not spawn anything; call Start.
func New(opts Options) (*Supervisor, error) {
	if opts.Locations == nil {
		return nil, errors.New("supervisor: Locations is required")
	}
	if opts.File == nil {
		return nil, errors.New("supervisor: File is required")
	}
	if len(opts.Order) == 0 {
		return nil, errors.New("supervisor: Order is required")
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.GracefulStopTimeout <= 0 {
		opts.GracefulStopTimeout = DefaultGracefulStopTimeout
	}
	if opts.Backoff.Base <= 0 {
		opts.Backoff = DefaultBackoff()
	}
	if opts.BaseDir == "" {
		opts.BaseDir = "."
	}

	services := make(map[string]*serviceRuntime, len(opts.Order))
	order := make([]string, 0, len(opts.Order))
	for _, name := range opts.Order {
		svc, ok := opts.File.Services[name]
		if !ok {
			return nil, fmt.Errorf("supervisor: order references unknown service %q", name)
		}
		rt := &serviceRuntime{
			spec:   svc,
			name:   name,
			status: StatusStarting,
			done:   make(chan struct{}),
		}
		services[name] = rt
		order = append(order, name)
	}

	return &Supervisor{
		opts:     opts,
		services: services,
		order:    order,
		stopCh:   make(chan struct{}),
	}, nil
}

// Start launches every service in topological order and returns once all have
// been spawned. It does not block until services exit — use Wait for that.
// Calling Start twice returns an error.
func (s *Supervisor) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("supervisor: already started")
	}

	if err := s.openLoggers(); err != nil {
		return err
	}

	if s.opts.InstallSignalHandler {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			select {
			case <-sigCh:
				ctx2, cancel := context.WithTimeout(context.Background(), s.opts.GracefulStopTimeout)
				defer cancel()
				_ = s.Stop(ctx2)
			case <-s.stopCh:
			}
		}()
	}

	for _, name := range s.order {
		rt := s.services[name]
		// Unless-stopped services with a persisted "stopped" marker are
		// skipped so they don't auto-resume on the next `up`.
		if rt.spec.Restart == config.RestartUnlessStopped && s.hasStoppedMarker(name) {
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			close(rt.done)
			continue
		}
		s.wg.Add(1)
		go s.runService(ctx, rt)
	}
	return nil
}

// openLoggers opens the per-service log files under the state logs dir.
func (s *Supervisor) openLoggers() error {
	if err := s.opts.Locations.MkdirAll(); err != nil {
		return err
	}
	for _, name := range s.order {
		rt := s.services[name]
		path := filepath.Join(s.opts.Locations.LogsDir, name+".log")
		l, err := newServiceLogger(path, name, s.opts.Stdout, s.opts.Foreground)
		if err != nil {
			return fmt.Errorf("open log for %q: %w", name, err)
		}
		rt.logger = l
	}
	return nil
}

// Wait blocks until all service run-loop goroutines have exited (every
// service has exited or been stopped and no restart is pending).
func (s *Supervisor) Wait() {
	s.wg.Wait()
}

// runService is the per-service supervise loop: launch, wait, apply restart
// policy with backoff, repeat until stopped or the policy gives up.
func (s *Supervisor) runService(ctx context.Context, rt *serviceRuntime) {
	defer s.wg.Done()
	defer close(rt.done)

	for {
		if s.isStopping() || rt.stopped.Load() {
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			return
		}

		cmd, pipeWG, err := s.launch(rt)
		if err != nil {
			rt.logger.writeLine(fmt.Sprintf("local-compose: failed to start: %v", err))
			rt.mu.Lock()
			rt.status = StatusBackoff
			rt.mu.Unlock()
			if !s.shouldRetry(rt) {
				return
			}
			if !s.sleepBackoff(ctx, rt) {
				return
			}
			continue
		}

		waitErr := cmd.Wait()
		pipeWG.Wait()

		exitCode := exitCodeFrom(waitErr)
		if waitErr != nil {
			rt.logger.writeLine(fmt.Sprintf("local-compose: exited with code %d", exitCode))
		}

		rt.mu.Lock()
		rt.pid = 0
		rt.pgid = 0
		rt.exitCode = exitCode
		rt.status = StatusExited
		rt.finishedAt = time.Now()
		rt.mu.Unlock()

		if s.isStopping() || rt.stopped.Load() {
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			return
		}
		if !shouldRestart(rt.spec.Restart, exitCode) {
			return
		}
		if !s.shouldRetry(rt) {
			rt.logger.writeLine("local-compose: giving up after max restart attempts")
			return
		}
		if !s.sleepBackoff(ctx, rt) {
			return
		}
	}
}

// launch starts one process for the service and spins up pipe-reading
// goroutines. It returns the cmd (for Wait), a WaitGroup that completes when
// the pipe readers have drained, and any start error.
func (s *Supervisor) launch(rt *serviceRuntime) (*command, *sync.WaitGroup, error) {
	svc := rt.spec
	shell := svc.Shell
	if shell == "" {
		shell = config.DefaultShell
	}

	cmd := newCommand(shell, "-c", svc.Command)
	cmd.dir = resolveWorkingDir(s.opts.BaseDir, svc.WorkingDir)
	cmd.env = mergeEnv(os.Environ(), svc.Env)
	if err := applyProcessGroup(cmd); err != nil {
		return nil, nil, fmt.Errorf("set process group: %w", err)
	}

	stdout, stderr, err := cmd.pipes()
	if err != nil {
		return nil, nil, err
	}

	if err := cmd.start(); err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}

	pgid := cmd.processPID() // Setpgid makes pgid == pid

	rt.mu.Lock()
	rt.pid = cmd.processPID()
	rt.pgid = pgid
	rt.status = StatusRunning
	rt.startedAt = time.Now()
	rt.mu.Unlock()

	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go s.pipeLines(stdout, rt, &pipeWG)
	go s.pipeLines(stderr, rt, &pipeWG)

	return cmd, &pipeWG, nil
}

// pipeLines reads a child pipe line-by-line and forwards to the service logger.
func (s *Supervisor) pipeLines(r io.ReadCloser, rt *serviceRuntime, wg *sync.WaitGroup) {
	defer wg.Done()
	s.readLines(r, rt)
}

// Stop gracefully stops every service: marks them stopped, SIGTERMs each
// group, waits up to GracefulStopTimeout, then SIGKILLs any survivors. It
// also writes "stopped" marker files for unless-stopped services so they are
// not auto-resumed on the next `up`.
func (s *Supervisor) Stop(ctx context.Context) error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	s.stopOnce.Do(func() { close(s.stopCh) })

	for _, name := range s.order {
		rt := s.services[name]
		if rt.spec.Restart == config.RestartUnlessStopped {
			_ = s.writeStoppedMarker(name)
		}
	}

	s.signalAll(syscall.SIGTERM)

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()

	graceful := s.opts.GracefulStopTimeout
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < graceful {
			graceful = remaining
		}
	}

	select {
	case <-done:
		return nil
	case <-time.After(graceful):
	}

	s.signalAll(syscall.SIGKILL)
	<-done
	return nil
}

// signalAll sends sig to every running service's process group.
func (s *Supervisor) signalAll(sig syscall.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rt := range s.services {
		rt.mu.Lock()
		pgid := rt.pgid
		rt.mu.Unlock()
		if pgid > 0 {
			_ = killGroup(pgid, sig)
		}
	}
}

// StopService stops a single service by name. If markStopped is true it
// writes the unless-stopped marker so the service won't auto-resume. It
// blocks until the service's run loop has exited (or the grace period
// elapses, after which the group is SIGKILLed).
func (s *Supervisor) StopService(name string, markStopped bool) error {
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	return s.stopOne(rt, markStopped, s.opts.GracefulStopTimeout)
}

// stopOne stops a single service and waits for its run loop to exit.
func (s *Supervisor) stopOne(rt *serviceRuntime, markStopped bool, grace time.Duration) error {
	if rt.stopped.Swap(true) {
		// Already stopped; just wait for the loop to finish if running.
		select {
		case <-rt.done:
		default:
		}
		return nil
	}

	if markStopped && rt.spec.Restart == config.RestartUnlessStopped {
		_ = s.writeStoppedMarker(rt.name)
	}

	rt.mu.Lock()
	pgid := rt.pgid
	rt.mu.Unlock()
	if pgid > 0 {
		_ = killGroup(pgid, syscall.SIGTERM)
	}

	select {
	case <-rt.done:
		return nil
	case <-time.After(grace):
	}

	rt.mu.Lock()
	pgid = rt.pgid
	rt.mu.Unlock()
	if pgid > 0 {
		_ = killGroup(pgid, syscall.SIGKILL)
	}
	select {
	case <-rt.done:
	case <-time.After(2 * time.Second):
	}
	return nil
}

// Restart stops a single service (without persisting a stop marker) and then
// launches a fresh run loop for it.
func (s *Supervisor) Restart(name string) error {
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	if err := s.stopOne(rt, false, s.opts.GracefulStopTimeout); err != nil {
		return err
	}

	// Reset state for a clean relaunch.
	rt.stopped.Store(false)
	_ = s.removeStoppedMarker(name)
	rt.done = make(chan struct{})
	s.wg.Add(1)
	go s.runService(context.Background(), rt)
	return nil
}

// States returns a snapshot of every service's state, sorted by start order.
func (s *Supervisor) States() []ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServiceState, 0, len(s.order))
	for _, name := range s.order {
		rt := s.services[name]
		rt.mu.Lock()
		health := "n/a"
		if rt.spec.Healthcheck != nil {
			health = "starting"
		}
		out = append(out, ServiceState{
			Name:       name,
			Status:     rt.status,
			PID:        rt.pid,
			ExitCode:   rt.exitCode,
			Restarts:   rt.restarts,
			StartedAt:  rt.startedAt,
			FinishedAt: rt.finishedAt,
			HasHealth:  rt.spec.Healthcheck != nil,
			Health:     health,
		})
		rt.mu.Unlock()
	}
	return out
}

// LogPath returns the absolute path of a service's log file.
func (s *Supervisor) LogPath(name string) (string, error) {
	if _, ok := s.services[name]; !ok {
		return "", fmt.Errorf("supervisor: unknown service %q", name)
	}
	return filepath.Join(s.opts.Locations.LogsDir, name+".log"), nil
}

// isStopping reports whether a global Stop has been initiated.
func (s *Supervisor) isStopping() bool {
	return s.stopped.Load()
}

// shouldRetry reports whether the service is still within its restart attempt
// budget (always/unless-stopped are unlimited; on-failure is capped).
func (s *Supervisor) shouldRetry(rt *serviceRuntime) bool {
	if rt.spec.Restart == config.RestartAlways || rt.spec.Restart == config.RestartUnlessStopped {
		return true
	}
	return rt.restarts < s.opts.Backoff.MaxAttempts
}

// sleepBackoff waits the next backoff delay for rt, incrementing the restart
// counter. It returns false if the supervisor is stopping or the context is
// cancelled before the delay elapses.
func (s *Supervisor) sleepBackoff(ctx context.Context, rt *serviceRuntime) bool {
	rt.mu.Lock()
	rt.restarts++
	rt.status = StatusBackoff
	delay := s.opts.Backoff.delay(rt.restarts)
	rt.mu.Unlock()

	if delay < 0 {
		delay = 0
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.stopCh:
		return false
	case <-rt.done:
		return false
	case <-t.C:
		return true
	}
}

// Close releases per-service resources (log files). It is safe to call after
// Stop.
func (s *Supervisor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	names := make([]string, 0, len(s.services))
	for n := range s.services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if l := s.services[n].logger; l != nil {
			if err := l.close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
