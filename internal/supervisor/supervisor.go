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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/health"
	"github.com/blesswinsamuel/local-compose/internal/procstat"
	"github.com/blesswinsamuel/local-compose/internal/project"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
	"github.com/blesswinsamuel/local-compose/internal/ui"
)

// Status is the lifecycle state of a supervised service.
type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusBackoff  Status = "backoff"
	StatusExited   Status = "exited"
	StatusStopped  Status = "stopped" // explicitly stopped; not restarted
)

// errSupervisorStopping is returned by waitForDep when the supervisor is
// shutting down (Stop called or context cancelled) while a dependent is still
// waiting on a dependency. User-initiated cancellation is not a failure, so the
// supervisor does NOT record it as failed.
var errSupervisorStopping = errors.New("supervisor stopping")

// DefaultGracefulStopTimeout is how long Stop waits for SIGTERM before SIGKILL.
const DefaultGracefulStopTimeout = 20 * time.Second

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

	// Env holds variables from the project's env file, layered under each
	// service's own env. It is applied on top of the parent environment
	// (service env still wins). May be nil.
	Env []string

	// InstallSignalHandler, when true, makes Start install a SIGINT/SIGTERM
	// handler that triggers a graceful Stop. The daemon and foreground CLI
	// enable this; tests leave it off.
	InstallSignalHandler bool

	// Selected, when non-empty, is the subset of Order to launch at Start.
	// Services not selected are left in the stopped state (no run loop) so
	// `ps` still lists them but only the selected services run. When empty,
	// every service is started. Used by `start <service>` on a stopped
	// project to materialize a supervisor for just one service (and its
	// depends_on chain). A service selected later via StartService/Restart
	// launches normally.
	Selected []string
}

// ServiceState is a point-in-time snapshot of a service used by ps/web.
type ServiceState struct {
	Name       string
	Status     Status
	PID        int
	ExitCode   int
	Restarts   int
	StartedAt  time.Time
	FinishedAt time.Time
	HasHealth  bool
	Health     string // "n/a", or health.State string when HasHealth
}

// TopStat is a per-service resource snapshot for the `top` command. CPU is a
// percentage of one core averaged over TopSampleInterval and can exceed 100 on
// multi-core work; RSS is the aggregate resident set size of every process in
// the service's process group.
type TopStat struct {
	Name   string
	Status Status
	PID    int
	PGID   int
	Procs  int
	CPU    float64
	RSS    uint64
}

// TopSampleInterval is how long `top` samples a project's process groups: two
// snapshots are taken this far apart to derive CPU usage.
const TopSampleInterval = time.Second

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

	// startedOnce is set the first time the service successfully launches and
	// transitions to running. depends_on: service_started waiters poll it.
	startedOnce atomic.Bool

	// checker is the per-service health checker (nil when the service has no
	// healthcheck). Guarded by checkerMu so dependents and States can read it
	// without contending with the run loop's main mutex.
	checkerMu sync.Mutex
	checker   *health.Checker

	// done is closed when the service's runService goroutine exits.
	done chan struct{}

	// ptyMaster is the master fd of the PTY when the service has tty: true.
	// nil for non-TTY services. Guarded by ptyMu.
	ptyMu     sync.Mutex
	ptyMaster *os.File
}

// setChecker stores/clears the service's health checker under checkerMu.
func (rt *serviceRuntime) setChecker(c *health.Checker) {
	rt.checkerMu.Lock()
	rt.checker = c
	rt.checkerMu.Unlock()
}

// getChecker returns the current health checker (may be nil).
func (rt *serviceRuntime) getChecker() *health.Checker {
	rt.checkerMu.Lock()
	defer rt.checkerMu.Unlock()
	return rt.checker
}

func (rt *serviceRuntime) setSpec(s config.Service) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.spec = s
}

func (rt *serviceRuntime) getSpec() config.Service {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.spec
}

// Supervisor owns and supervises a set of services.
type Supervisor struct {
	opts Options

	mu       sync.Mutex
	services map[string]*serviceRuntime
	order    []string

	// selected, when non-empty, is the set of services to launch at Start.
	// See Options.Selected. Services not in it are skipped at Start.
	selected map[string]bool

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	started atomic.Bool
	stopped atomic.Bool

	// failed is set when a service could not start because a depends_on
	// condition was not satisfied (e.g. a dependency went unhealthy or exited
	// before becoming healthy). Foreground `up` surfaces it as a non-zero exit.
	failed atomic.Bool
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

	var selected map[string]bool
	if len(opts.Selected) > 0 {
		selected = make(map[string]bool, len(opts.Selected))
		for _, name := range opts.Selected {
			if _, ok := services[name]; !ok {
				return nil, fmt.Errorf("supervisor: selected references unknown service %q", name)
			}
			selected[name] = true
		}
	}

	return &Supervisor{
		opts:     opts,
		services: services,
		order:    order,
		selected: selected,
		stopCh:   make(chan struct{}),
	}, nil
}

// UpdateFile updates the supervisor's parsed config file (e.g. when config changes on disk).
func (s *Supervisor) UpdateFile(file *config.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.File = file
}

// Reconcile updates the supervisor's parsed config file and service set.
// Services present in the supervisor but absent from file.Services are
// treated as orphans: if removeOrphans is true, they are cleanly stopped
// and unregistered from the supervisor. New services present in file.Services
// are added to the runtime map. Specs for existing services are updated.
func (s *Supervisor) Reconcile(file *config.File, order []string, removeOrphans bool) error {
	s.mu.Lock()
	s.opts.File = file

	var orphans []*serviceRuntime
	var orphanNames []string

	for name, rt := range s.services {
		if _, ok := file.Services[name]; !ok {
			orphans = append(orphans, rt)
			orphanNames = append(orphanNames, name)
		}
	}
	s.mu.Unlock()

	if len(orphans) > 0 {
		if removeOrphans {
			for _, rt := range orphans {
				_ = s.stopOne(rt, s.opts.GracefulStopTimeout)
				if rt.logger != nil {
					rt.logger.writeLine(fmt.Sprintf("%s %s %s", ui.Dim("local-compose:"), ui.StatusMessage("stopped orphan service"), ui.Dim(rt.name)))
					_ = rt.logger.close()
				}
			}

			s.mu.Lock()
			for _, name := range orphanNames {
				delete(s.services, name)
			}
			newOrderList := make([]string, 0, len(s.order)-len(orphanNames))
			for _, name := range s.order {
				if _, ok := file.Services[name]; ok {
					newOrderList = append(newOrderList, name)
				}
			}
			s.order = newOrderList
			s.mu.Unlock()
		} else {
			if s.opts.Stdout != nil {
				sort.Strings(orphanNames)
				_, _ = fmt.Fprintf(s.opts.Stdout, "%s Found orphan services: %s. Use --remove-orphans to stop them.\n", ui.Dim("local-compose:"), strings.Join(orphanNames, ", "))
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, name := range order {
		svc, ok := file.Services[name]
		if !ok {
			continue
		}
		rt, exists := s.services[name]
		if exists {
			rt.setSpec(svc)
		} else {
			doneCh := make(chan struct{})
			close(doneCh)
			rt = &serviceRuntime{
				spec:   svc,
				name:   name,
				status: StatusStopped,
				done:   doneCh,
			}
			if s.started.Load() && s.opts.Locations != nil {
				path := filepath.Join(s.opts.Locations.LogsDir, name+".log")
				if logger, err := newServiceLogger(path, name, s.opts.Stdout, s.opts.Foreground); err == nil {
					rt.logger = logger
				}
			}
			s.services[name] = rt
		}
	}

	s.order = append([]string(nil), order...)
	return nil
}

// AdoptOrStart attaches to running services described in snapshot if their
// PIDs/PGIDs match live processes, and starts any that aren't running.
func (s *Supervisor) AdoptOrStart(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("supervisor: already started")
	}

	if err := s.openLoggers(); err != nil {
		return err
	}

	snap, err := LoadState(s.opts.Locations)
	if err != nil && s.opts.Stdout != nil {
		fmt.Fprintf(s.opts.Stdout, "supervisor: load state: %v\n", err)
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
		if s.selected != nil && !s.selected[name] {
			// Not selected for this run (lazy `start <service>`): leave the
			// service stopped so `ps` still lists it, but launch no run loop.
			if rt.logger != nil {
				rt.logger.writeLine(fmt.Sprintf("local-compose: skipping service %s", name))
			}
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			close(rt.done)
			continue
		}

		var adoptedSnap *ServiceStateSnapshot
		if snap != nil {
			if sSnap, ok := snap.Services[name]; ok {
				if IsProcessGroupAlive(sSnap.PGID) {
					sSnapCopy := sSnap
					adoptedSnap = &sSnapCopy
				}
			}
		}

		s.wg.Add(1)
		if adoptedSnap != nil {
			go s.adoptService(ctx, rt, *adoptedSnap)
		} else {
			go s.runService(ctx, rt)
		}
	}
	return nil
}

// adoptService attaches to an existing running process group recorded in snapshot and monitors it.
func (s *Supervisor) adoptService(ctx context.Context, rt *serviceRuntime, snap ServiceStateSnapshot) {
	defer s.wg.Done()
	defer close(rt.done)

	rt.mu.Lock()
	rt.pid = snap.PID
	rt.pgid = snap.PGID
	rt.status = StatusRunning
	rt.startedAt = snap.StartedAt
	rt.restarts = snap.Restarts
	rt.mu.Unlock()
	rt.startedOnce.Store(true)

	if rt.logger != nil {
		rt.logger.writeLine(fmt.Sprintf("local-compose: adopted existing process group (PGID %d)", snap.PGID))
	}

	var chk *health.Checker
	if rt.spec.Healthcheck != nil {
		var err error
		chk, err = health.New(rt.name, s.healthConfig(rt), func(line string) {
			if rt.logger != nil {
				rt.logger.writeLine(line)
			}
		})
		if err == nil {
			rt.setChecker(chk)
			chk.EnsureStarted(ctx)
			defer func() {
				chk.Stop()
				rt.setChecker(nil)
			}()
		}
	}

	// Poll process group liveness until exit
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if s.isStopping() || rt.stopped.Load() {
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			return
		}

		if !IsProcessGroupAlive(snap.PGID) {
			rt.logger.writeLine(fmt.Sprintf("%s %s %s", ui.Dim("local-compose:"), ui.StatusMessage("adopted process exited"), ui.Dim("at "+time.Now().UTC().Format(time.RFC3339))))
			rt.mu.Lock()
			rt.pid = 0
			rt.pgid = 0
			rt.finishedAt = time.Now()
			rt.status = StatusExited
			rt.mu.Unlock()

			if s.isStopping() || rt.stopped.Load() {
				return
			}
			if !shouldRestart(rt.spec.Restart, -1) {
				return
			}
			if !s.shouldRetry(rt) {
				rt.logger.writeLine("local-compose: giving up after max restart attempts")
				return
			}
			if !s.sleepBackoff(ctx, rt) {
				return
			}
			// Switch to standard run loop after adoption exit
			s.wg.Add(1)
			s.runService(ctx, rt)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
	}
}

// Start launches every service in topological order and returns once all have
// been spawned. It does not block until services exit — use Wait for that.
// Calling Start twice returns an error.
func (s *Supervisor) Start(ctx context.Context) error {
	return s.AdoptOrStart(ctx)
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
// policy with backoff, repeat until stopped or the policy gives up. It first
// waits for the service's depends_on conditions (service_started /
// service_healthy) to be satisfied, and starts a health checker for services
// that declare a healthcheck so dependents and `ps` see real health state.
func (s *Supervisor) runService(ctx context.Context, rt *serviceRuntime) {
	defer s.wg.Done()
	defer close(rt.done)

	// Build the health checker up front (without probing) so dependents
	// waiting on service_healthy observe a real checker in StateStarting
	// rather than a nil one.
	var chk *health.Checker
	if rt.spec.Healthcheck != nil {
		var err error
		chk, err = health.New(rt.name, s.healthConfig(rt), func(line string) {
			if rt.logger != nil {
				rt.logger.writeLine(line)
			}
		})
		if err != nil {
			if rt.logger != nil {
				rt.logger.writeLine(fmt.Sprintf("local-compose: invalid healthcheck: %v", err))
			}
			rt.mu.Lock()
			rt.status = StatusStopped
			rt.mu.Unlock()
			s.failed.Store(true)
			return
		}
		rt.setChecker(chk)
		defer func() {
			chk.Stop()
			rt.setChecker(nil)
		}()
	}

	// Block until dependencies satisfy their conditions before launching.
	if err := s.waitForDeps(ctx, rt); err != nil {
		cancelled := errors.Is(err, errSupervisorStopping)
		if rt.logger != nil {
			rt.logger.writeLine(fmt.Sprintf("local-compose: not starting: %v", err))
		}
		rt.mu.Lock()
		rt.status = StatusStopped
		rt.mu.Unlock()
		// A genuine dependency failure (it exited or went unhealthy before
		// satisfying the condition) is recorded as a failure; a
		// user-initiated shutdown (errSupervisorStopping) is not.
		if !cancelled {
			s.failed.Store(true)
		}
		return
	}

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

		// The service is up; begin probing (idempotent across restarts).
		if chk != nil {
			chk.EnsureStarted(ctx)
		}

		// Drain the stdout/stderr pipes BEFORE calling Wait. Per the os/exec
		// docs, calling Wait before all reads from a StdoutPipe/StderrPipe
		// have completed can close the pipe while the final line is still in
		// the kernel pipe buffer, losing it. The readers EOF once the child
		// closes its end (on exit), at which point Wait reaps the process.
		pipeWG.Wait()
		waitErr := cmd.Wait()
		rt.closePTY()

		exitCode := exitCodeFrom(waitErr)
		if waitErr != nil {
			rt.logger.writeLine(fmt.Sprintf("%s %s %s with exit code %d", ui.Dim("local-compose:"), ui.StatusMessage("exited"), ui.Dim("at "+time.Now().UTC().Format(time.RFC3339)), exitCode))
		}

		stopping := s.isStopping() || rt.stopped.Load()

		rt.mu.Lock()
		rt.pid = 0
		rt.pgid = 0
		rt.exitCode = exitCode
		rt.finishedAt = time.Now()
		if stopping {
			rt.status = StatusStopped
		} else {
			rt.status = StatusExited
		}
		rt.mu.Unlock()

		if stopping {
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

// healthConfig translates the service's Healthcheck spec into a health.Config
// for the checker, inheriting the service's shell, working_dir, and env so a
// CMD-SHELL probe runs in the same context as the service.
func (s *Supervisor) healthConfig(rt *serviceRuntime) health.Config {
	hc := rt.spec.Healthcheck
	return health.Config{
		Test:       hc.Test,
		Interval:   hc.Interval,
		Retries:    hc.Retries,
		Timeout:    hc.Timeout,
		Shell:      rt.spec.Shell,
		WorkingDir: resolveWorkingDir(s.opts.BaseDir, rt.spec.WorkingDir),
		Env:        mergeEnv(mergeEnvSlice(os.Environ(), s.opts.Env), rt.spec.Env),
	}
}

// waitForDeps blocks until every depends_on entry's condition is satisfied.
// Returns an error if a condition can never be met (the dependency exited
// permanently, or went unhealthy for service_healthy) or the supervisor is
// stopping.
func (s *Supervisor) waitForDeps(ctx context.Context, rt *serviceRuntime) error {
	for _, depName := range rt.spec.DependsOn.Order {
		entry, ok := rt.spec.DependsOn.Entries[depName]
		if !ok {
			continue
		}
		if err := s.waitForDep(ctx, depName, entry.Condition); err != nil {
			return err
		}
	}
	return nil
}

// depPollInterval is how often waitForDep re-checks the dependency's state.
// Kept small so service_healthy dependents start promptly once a dependency
// flips healthy without busy-looping.
const depPollInterval = 20 * time.Millisecond

// waitForDep polls depName until condition is satisfied. service_started is
// satisfied once the dependency has launched at least once; service_healthy
// once its health checker reports healthy (and fails fast on unhealthy).
func (s *Supervisor) waitForDep(ctx context.Context, depName string, cond config.DependsOnCondition) error {
	dep, ok := s.services[depName]
	if !ok {
		return fmt.Errorf("depends_on %q: unknown service", depName)
	}

	ticker := time.NewTicker(depPollInterval)
	defer ticker.Stop()
	for {
		if s.isStopping() {
			return errSupervisorStopping
		}
		switch cond {
		case config.ConditionServiceStarted:
			if dep.startedOnce.Load() {
				return nil
			}
		case config.ConditionServiceHealthy:
			if chk := dep.getChecker(); chk != nil {
				switch chk.State() {
				case health.StateHealthy:
					return nil
				case health.StateUnhealthy:
					return fmt.Errorf("dependency %q is unhealthy", depName)
				}
			} else if dep.startedOnce.Load() {
				// No healthcheck declared (config validation normally
				// forbids this for service_healthy); fall back to started.
				return nil
			}
		}

		// If the dependency's run loop has exited for good, the condition
		// can no longer be satisfied — fail rather than hang forever.
		select {
		case <-dep.done:
			return fmt.Errorf("dependency %q exited before satisfying %s", depName, cond)
		default:
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", errSupervisorStopping, ctx.Err())
		case <-s.stopCh:
			return errSupervisorStopping
		case <-ticker.C:
		}
	}
}

// Failed reports whether any service failed to start because a depends_on
// condition could not be satisfied. Foreground `up` surfaces this as a
// non-zero exit so CI catches misconfigured health gates.
func (s *Supervisor) Failed() bool {
	return s.failed.Load()
}

// launch starts one process for the service and spins up pipe-reading
// goroutines. It returns the cmd (for Wait), a WaitGroup that completes when
// the pipe readers have drained, and any start error.
func (s *Supervisor) launch(rt *serviceRuntime) (*command, *sync.WaitGroup, error) {
	svc := rt.getSpec()
	shell := svc.Shell
	if shell == "" {
		shell = config.DefaultShell
	}

	cmd := newCommand(shell, "-c", svc.Command)
	cmd.dir = resolveWorkingDir(s.opts.BaseDir, svc.WorkingDir)
	cmd.env = mergeEnv(applyEnvDefaults(mergeEnvSlice(os.Environ(), s.opts.Env), defaultColorEnv()), svc.Env)

	if svc.TTY {
		return s.launchPTY(cmd, rt)
	}

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

	// The spawn succeeded, so the previous run is over: move its log to
	// <name>.prev.log and start a fresh current file for this run. Best-effort
	// so rotation never blocks a start; a spawn failure above leaves the
	// previous run's log untouched.
	rt.logger.rotate()

	pgid := cmd.processPID() // Setpgid makes pgid == pid

	rt.mu.Lock()
	rt.pid = cmd.processPID()
	rt.pgid = pgid
	rt.status = StatusRunning
	rt.startedAt = time.Now()
	rt.mu.Unlock()
	rt.startedOnce.Store(true)

	_ = s.SaveState()

	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go s.pipeLines(stdout, rt, &pipeWG)
	go s.pipeLines(stderr, rt, &pipeWG)

	return cmd, &pipeWG, nil
}

// launchPTY starts a service with a PTY. Output is read from the master fd
// and forwarded to the service logger. The master is stored on the runtime
// so Attach clients can read/write directly.
func (s *Supervisor) launchPTY(cmd *command, rt *serviceRuntime) (*command, *sync.WaitGroup, error) {
	ptyMaster, err := cmd.startWithPTY()
	if err != nil {
		return nil, nil, fmt.Errorf("start with PTY: %w", err)
	}

	// Same rotation as launch(): the new run starts a fresh log file.
	rt.logger.rotate()

	pgid := cmd.processPID()

	rt.mu.Lock()
	rt.pid = cmd.processPID()
	rt.pgid = pgid
	rt.status = StatusRunning
	rt.startedAt = time.Now()
	rt.mu.Unlock()
	rt.startedOnce.Store(true)

	rt.ptyMu.Lock()
	rt.ptyMaster = ptyMaster
	rt.ptyMu.Unlock()

	var pipeWG sync.WaitGroup
	pipeWG.Add(1)
	go s.readPTY(ptyMaster, rt, &pipeWG)

	return cmd, &pipeWG, nil
}

// readPTY reads from the PTY master and forwards output to the service logger.
func (s *Supervisor) readPTY(r io.Reader, rt *serviceRuntime, wg *sync.WaitGroup) {
	defer wg.Done()
	s.readLines(r, rt)
}

// pipeLines reads a child pipe line-by-line and forwards to the service logger.
func (s *Supervisor) pipeLines(r io.ReadCloser, rt *serviceRuntime, wg *sync.WaitGroup) {
	defer wg.Done()
	s.readLines(r, rt)
}

// Stop gracefully stops every service: marks them stopped, SIGTERMs each
// group, waits up to GracefulStopTimeout, then SIGKILLs any survivors.
func (s *Supervisor) Stop(ctx context.Context) error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	s.stopOnce.Do(func() { close(s.stopCh) })

	// Mark every eligible service as stopping immediately so clients see
	// the transition before the process actually exits.
	for _, name := range s.order {
		rt := s.services[name]
		rt.mu.Lock()
		switch rt.status {
		case StatusStarting, StatusRunning, StatusBackoff:
			rt.status = StatusStopping
		}
		rt.mu.Unlock()
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
		_ = RemoveState(s.opts.Locations)
		return nil
	case <-time.After(graceful):
	}

	s.signalAll(syscall.SIGKILL)
	<-done
	_ = RemoveState(s.opts.Locations)
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

// StopService stops a single service by name. It blocks until the service's
// run loop has exited (or the grace period elapses, after which the group is
// SIGKILLed).
func (s *Supervisor) StopService(name string) error {
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	if err := s.stopOne(rt, s.opts.GracefulStopTimeout); err != nil {
		return err
	}
	rt.logger.writeLine(fmt.Sprintf("%s %s %s", ui.Dim("local-compose:"), ui.StatusMessage("stopped"), ui.Dim("at "+time.Now().UTC().Format(time.RFC3339))))
	return nil
}

// KillService immediately sends signal to a single service's process group
// without a grace period. signal is a signal name ("SIGKILL", "SIGTERM", ...);
// empty means SIGKILL. If the service has already exited, this is a no-op.
func (s *Supervisor) KillService(name, signal string) error {
	sig, err := parseSignal(signal)
	if err != nil {
		return err
	}
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	if rt.stopped.Swap(true) {
		select {
		case <-rt.done:
		default:
		}
		return nil
	}
	rt.mu.Lock()
	switch rt.status {
	case StatusStarting, StatusRunning, StatusBackoff:
		rt.status = StatusStopping
	}
	pgid := rt.pgid
	rt.mu.Unlock()
	if pgid > 0 {
		_ = killGroup(pgid, sig)
	}
	select {
	case <-rt.done:
	case <-time.After(5 * time.Second):
	}
	rt.logger.writeLine(fmt.Sprintf("%s %s %s", ui.Dim("local-compose:"), ui.StatusMessage("killed"), ui.Dim("at "+time.Now().UTC().Format(time.RFC3339))))
	return nil
}

// stopOne stops a single service and waits for its run loop to exit.
func (s *Supervisor) stopOne(rt *serviceRuntime, grace time.Duration) error {
	alreadyStopped := rt.stopped.Swap(true)
	if alreadyStopped {
		// Already marked as stopped — but if the process is still
		// running, resend the stop signal so the user can re-trigger
		// graceful shutdown (or escalation after the grace period).
		select {
		case <-rt.done:
			return nil
		default:
		}
	}

	// Mark as stopping immediately so clients see the transition.
	rt.mu.Lock()
	switch rt.status {
	case StatusStarting, StatusRunning, StatusBackoff:
		rt.status = StatusStopping
	}
	rt.mu.Unlock()

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

// Restart stops a single service and then launches a fresh run loop for it.
func (s *Supervisor) Restart(name string) error {
	if s.isStopping() {
		return fmt.Errorf("supervisor: stopped")
	}
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	if err := s.stopOne(rt, s.opts.GracefulStopTimeout); err != nil {
		return err
	}

	rt.logger.writeLine(fmt.Sprintf("%s %s %s", ui.Dim("local-compose:"), ui.StatusMessage("restarted"), ui.Dim("at "+time.Now().UTC().Format(time.RFC3339))))

	// Reset state for a clean relaunch.
	rt.stopped.Store(false)
	rt.done = make(chan struct{})
	s.wg.Add(1)
	go s.runService(context.Background(), rt)
	return nil
}

// StartService starts one service that is currently stopped or exited,
// leaving any other stopped services alone. Services that are starting,
// running, backing off, or stopping are left as-is. Used by `start
// <service>`: on a running project it resumes the service in place, and on a
// lazily materialized supervisor it launches a previously skipped service.
func (s *Supervisor) StartService(name string) error {
	if s.isStopping() {
		return fmt.Errorf("supervisor: stopped")
	}
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	rt.mu.Lock()
	st := rt.status
	rt.mu.Unlock()
	switch st {
	case StatusStarting, StatusRunning, StatusBackoff:
		return nil
	case StatusStopping:
		return fmt.Errorf("supervisor: service %q is stopping", name)
	}
	return s.Restart(name)
}

// StartStopped resumes any services that are currently stopped or exited.
// Services that are starting, running, backing off, or stopping are left
// alone. Used by `up` when the project is already loaded in the daemon.
func (s *Supervisor) StartStopped() error {
	if s.isStopping() {
		return fmt.Errorf("supervisor: stopped")
	}
	for _, name := range s.order {
		rt := s.services[name]
		rt.mu.Lock()
		st := rt.status
		rt.mu.Unlock()
		switch st {
		case StatusStarting, StatusRunning, StatusBackoff, StatusStopping:
			continue
		}
		if err := s.Restart(name); err != nil {
			return err
		}
	}
	return nil
}

// States returns a snapshot of every service's state, sorted by start order.
func (s *Supervisor) States() []ServiceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServiceState, 0, len(s.order))
	for _, name := range s.order {
		rt := s.services[name]
		healthStr := "n/a"
		if rt.spec.Healthcheck != nil {
			if chk := rt.getChecker(); chk != nil {
				healthStr = string(chk.State())
			} else {
				healthStr = string(health.StateStarting)
			}
		}
		rt.mu.Lock()
		out = append(out, ServiceState{
			Name:       name,
			Status:     rt.status,
			PID:        rt.pid,
			ExitCode:   rt.exitCode,
			Restarts:   rt.restarts,
			StartedAt:  rt.startedAt,
			FinishedAt: rt.finishedAt,
			HasHealth:  rt.spec.Healthcheck != nil,
			Health:     healthStr,
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

// PreviousLogPath returns the absolute path of a service's previous-run log
// file (the run immediately before the current one).
func (s *Supervisor) PreviousLogPath(name string) (string, error) {
	if _, ok := s.services[name]; !ok {
		return "", fmt.Errorf("supervisor: unknown service %q", name)
	}
	return filepath.Join(s.opts.Locations.LogsDir, name+".prev.log"), nil
}

// Top returns a resource snapshot for one service (or every service when
// service is empty), used by the `top` command. All groups are sampled twice
// around a single shared interval so CPU usage is a fair delta rather than an
// average since process start. Services without a live process group report
// zero processes.
func (s *Supervisor) Top(service string) ([]TopStat, error) {
	s.mu.Lock()
	targets := make([]TopStat, 0, len(s.order))
	for _, name := range s.order {
		if service != "" && name != service {
			continue
		}
		rt, ok := s.services[name]
		if !ok {
			s.mu.Unlock()
			return nil, fmt.Errorf("supervisor: unknown service %q", name)
		}
		rt.mu.Lock()
		targets = append(targets, TopStat{
			Name:   rt.name,
			Status: rt.status,
			PID:    rt.pid,
			PGID:   rt.pgid,
		})
		rt.mu.Unlock()
	}
	s.mu.Unlock()

	first := make([]procstat.Sample, len(targets))
	for i, t := range targets {
		first[i], _ = procstat.SampleGroup(t.PGID)
	}
	time.Sleep(TopSampleInterval)
	second := make([]procstat.Sample, len(targets))
	for i, t := range targets {
		second[i], _ = procstat.SampleGroup(t.PGID)
	}

	for i := range targets {
		if first[i].Procs == 0 || second[i].Procs == 0 {
			continue
		}
		targets[i].Procs = second[i].Procs
		targets[i].RSS = second[i].RSS
		if d := second[i].CPU - first[i].CPU; d > 0 {
			targets[i].CPU = float64(d) / float64(TopSampleInterval) * 100
		}
	}
	return targets, nil
}

// isStopping reports whether a global Stop has been initiated.
func (s *Supervisor) isStopping() bool {
	return s.stopped.Load()
}

// shouldRetry reports whether the service is still within its restart attempt
// budget (always is unlimited; on-failure is capped).
func (s *Supervisor) shouldRetry(rt *serviceRuntime) bool {
	if rt.spec.Restart == config.RestartAlways {
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

// Attach returns the PTY master for a TTY service, allowing a client to read
// output and write input directly. Returns an error if the service is not
// TTY-enabled or is not running.
func (s *Supervisor) Attach(name string) (io.ReadWriteCloser, error) {
	rt, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("supervisor: unknown service %q", name)
	}
	if !rt.spec.TTY {
		return nil, fmt.Errorf("supervisor: service %q does not have tty enabled", name)
	}
	rt.ptyMu.Lock()
	master := rt.ptyMaster
	rt.ptyMu.Unlock()
	if master == nil {
		return nil, fmt.Errorf("supervisor: service %q is not running", name)
	}
	return master, nil
}

// WriteInput writes data to a TTY service's PTY master (stdin).
func (s *Supervisor) WriteInput(name string, data []byte) error {
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	rt.ptyMu.Lock()
	master := rt.ptyMaster
	rt.ptyMu.Unlock()
	if master == nil {
		return fmt.Errorf("supervisor: service %q is not running or not TTY", name)
	}
	_, err := master.Write(data)
	return err
}

// Resize changes the PTY window size for a TTY service.
func (s *Supervisor) Resize(name string, width, height int) error {
	rt, ok := s.services[name]
	if !ok {
		return fmt.Errorf("supervisor: unknown service %q", name)
	}
	rt.ptyMu.Lock()
	master := rt.ptyMaster
	rt.ptyMu.Unlock()
	if master == nil {
		return fmt.Errorf("supervisor: service %q is not running or not TTY", name)
	}
	return resizePTY(master, width, height)
}

// closePTY closes the PTY master fd for a service. Called when the process exits.
func (rt *serviceRuntime) closePTY() {
	rt.ptyMu.Lock()
	defer rt.ptyMu.Unlock()
	if rt.ptyMaster != nil {
		_ = rt.ptyMaster.Close()
		rt.ptyMaster = nil
	}
}

// Close releases per-service resources (log files, PTY fds). It is safe to call after
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
		s.services[n].closePTY()
		if l := s.services[n].logger; l != nil {
			if err := l.close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ListActionsFromFile returns a snapshot of the actions defined in file,
// sorted by name. It is shared with the orchestrator's stopped-project backend
// so a stopped project reports the same actions a running supervisor does.
func ListActionsFromFile(file *config.File) []protocol.ActionInfo {
	if file == nil || len(file.Actions) == 0 {
		return nil
	}
	names := make([]string, 0, len(file.Actions))
	for name := range file.Actions {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]protocol.ActionInfo, len(names))
	for i, name := range names {
		act := file.Actions[name]
		var deps []string
		for depName := range act.Spec.DependsOn.Entries {
			deps = append(deps, depName)
		}
		sort.Strings(deps)
		out[i] = protocol.ActionInfo{
			Name:       name,
			Command:    act.Spec.Command,
			WorkingDir: act.Spec.WorkingDir,
			TTY:        act.Spec.TTY,
			DependsOn:  deps,
		}
	}
	return out
}

// ListActions returns a snapshot of defined actions for the project.
func (s *Supervisor) ListActions() []protocol.ActionInfo {
	return ListActionsFromFile(s.opts.File)
}

// RunAction executes a named action command in a dedicated process group,
// streaming output lines to out. Returns the exit code of the action process.
func (s *Supervisor) RunAction(ctx context.Context, name string, extraArgs []string, out io.Writer) (int, error) {
	if s.opts.File == nil {
		return 1, fmt.Errorf("supervisor: no config file loaded")
	}
	act, ok := s.opts.File.Actions[name]
	if !ok {
		return 1, fmt.Errorf("action %q not found", name)
	}

	for _, depName := range act.Spec.DependsOn.Order {
		entry := act.Spec.DependsOn.Entries[depName]
		if rt, ok := s.services[depName]; ok {
			if rt.status == StatusStopped || rt.status == StatusExited || !rt.startedOnce.Load() {
				if err := s.Restart(depName); err != nil {
					return 1, fmt.Errorf("action %q: failed to start dependency service %q: %w", name, depName, err)
				}
			}
		}
		if err := s.waitForDep(ctx, depName, entry.Condition); err != nil {
			return 1, fmt.Errorf("action %q: dependency %q not ready: %w", name, depName, err)
		}
	}

	fullCmd := act.Spec.Command
	if len(extraArgs) > 0 {
		fullCmd += " " + strings.Join(extraArgs, " ")
	}

	workDir := resolveWorkingDir(s.opts.BaseDir, act.Spec.WorkingDir)

	baseEnv := config.BaseEnv(nil)
	if len(s.opts.Env) > 0 {
		baseEnv = mergeEnvSlice(os.Environ(), s.opts.Env)
	}
	env := config.BuildEnvOver(baseEnv, act.Spec.Env)

	shell := act.Spec.Shell
	if shell == "" {
		shell = config.DefaultShell
	}

	cmd := newCommand(shell, "-c", fullCmd)
	cmd.dir = workDir
	cmd.env = env
	applyProcessGroup(cmd)

	stdoutPipe, stderrPipe, err := cmd.pipes()
	if err != nil {
		return 1, fmt.Errorf("action %q: pipes: %w", name, err)
	}

	if err := cmd.start(); err != nil {
		return 1, fmt.Errorf("action %q: start: %w", name, err)
	}

	var logFile *os.File
	if s.opts.Locations != nil {
		actDir := filepath.Join(s.opts.Locations.State, "actions")
		if err := os.MkdirAll(actDir, 0o755); err == nil {
			logPath := filepath.Join(actDir, name+".log")
			prevPath := filepath.Join(actDir, name+".prev.log")
			if _, statErr := os.Stat(logPath); statErr == nil {
				_ = os.Rename(logPath, prevPath)
			}
			if f, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); openErr == nil {
				logFile = f
				defer func() { _ = logFile.Close() }()
			}
		}
	}

	var wg sync.WaitGroup
	var logMu sync.Mutex
	wg.Add(2)
	readLineStream := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if out != nil {
				_, _ = fmt.Fprintln(out, line)
			}
			if logFile != nil {
				logMu.Lock()
				_, _ = fmt.Fprintln(logFile, line)
				logMu.Unlock()
			}
		}
	}

	go readLineStream(stdoutPipe)
	go readLineStream(stderrPipe)

	wg.Wait()
	waitErr := cmd.Wait()
	code := exitCodeFrom(waitErr)
	return code, nil
}

// ActionLogPath returns the absolute path of an action's log file.
func (s *Supervisor) ActionLogPath(name string) (string, error) {
	if s.opts.Locations == nil {
		return "", fmt.Errorf("supervisor: no state location configured")
	}
	if s.opts.File == nil {
		return "", fmt.Errorf("supervisor: no config file loaded")
	}
	if _, ok := s.opts.File.Actions[name]; !ok {
		return "", fmt.Errorf("action %q not found", name)
	}
	path := filepath.Join(s.opts.Locations.State, "actions", name+".log")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("action %q has no log file yet", name)
	}
	return path, nil
}

// ActionPreviousLogPath returns the absolute path of an action's previous run log file.
func (s *Supervisor) ActionPreviousLogPath(name string) (string, error) {
	if s.opts.Locations == nil {
		return "", fmt.Errorf("supervisor: no state location configured")
	}
	if s.opts.File == nil {
		return "", fmt.Errorf("supervisor: no config file loaded")
	}
	if _, ok := s.opts.File.Actions[name]; !ok {
		return "", fmt.Errorf("action %q not found", name)
	}
	path := filepath.Join(s.opts.Locations.State, "actions", name+".prev.log")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("action %q has no previous log file", name)
	}
	return path, nil
}
