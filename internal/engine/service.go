package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"syscall"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/health"
	"github.com/blesswinsamuel/devyard/internal/logstore"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

const (
	defaultStopGrace = 10 * time.Second
	// stableRun is how long a run must last for the restart backoff to reset.
	stableRun = 10 * time.Second
	// maxOnFailureRetries bounds consecutive quick failures for on-failure.
	maxOnFailureRetries = 10
	backoffBase         = 500 * time.Millisecond
	backoffMax          = 30 * time.Second
	// stopSafety bounds how long Stop may take beyond the grace period if
	// the runner never reports the exit.
	stopSafety = 10 * time.Second
)

// Service is the actor handle of one service.
type Service struct {
	name  string
	inbox chan any
	done  chan struct{}
}

// Name returns the service name.
func (s *Service) Name() string { return s.name }

type svcStart struct {
	build bool
	reply chan error
}
type svcStop struct{ reply chan error }
type svcRestart struct {
	build bool
	reply chan error
}
type svcKill struct {
	sig   string
	reply chan error
}
type svcUpdate struct {
	def   *ProcessDef
	reply chan error
}
type svcShutdown struct {
	stop  bool
	reply chan error
}
type svcAttach struct {
	cols, rows int
	reply      chan attachResult
}
type attachResult struct {
	a   *runner.Attachment
	err error
}

type evPhase struct {
	run int64
	st  runner.Status
}
type evExited struct {
	run int64
	st  runner.Status
	err error
}
type evHealth struct {
	run    int64
	state  health.State
	detail string
}
type evBackoff struct{ gen int }
type evDeps struct {
	gen int
	err error
}
type evStopDeadline struct{ run int64 }

type serviceActor struct {
	h        *Service
	def      *ProcessDef
	launcher Launcher
	hub      *hub
	obs      Observer

	st      ServiceState
	desired bool

	proc     Proc
	run      int64
	runCtx   context.CancelFunc
	checker  *health.Checker
	stopping bool
	// restartAfterStop re-launches once the current run has stopped.
	restartAfterStop bool
	buildNext        bool
	stopWaiters      []chan error
	stopTimer        *time.Timer

	depsCancel context.CancelFunc
	depsGen    int

	backoffTimer *time.Timer
	backoffGen   int
	failures     int
	killedBy     string
}

// startService creates a service actor. It adopts a live run recorded in the
// service's process directory, and otherwise starts the service when desired
// is true (see initialize).
func startService(def *ProcessDef, launcher Launcher, h *hub, obs Observer, desired bool) *Service {
	svc := &Service{name: def.Name, inbox: make(chan any, 64), done: make(chan struct{})}
	a := &serviceActor{h: svc, def: def, launcher: launcher, hub: h, obs: obs, st: ServiceState{Status: StatusStopped}}
	go a.loop(desired)
	return svc
}

func (s *Service) send(ctx context.Context, msg any) error {
	select {
	case s.inbox <- msg:
		return nil
	case <-s.done:
		return fmt.Errorf("service %s: %w", s.name, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) call(ctx context.Context, msg any, reply chan error) error {
	if err := s.send(ctx, msg); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-s.done:
		return fmt.Errorf("service %s: %w", s.name, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start starts the service (after its dependencies). It returns once the
// start has been initiated.
func (s *Service) Start(ctx context.Context, build bool) error {
	r := make(chan error, 1)
	return s.call(ctx, svcStart{build: build, reply: r}, r)
}

// Stop stops the service and returns once its process is gone.
func (s *Service) Stop(ctx context.Context) error {
	r := make(chan error, 1)
	return s.call(ctx, svcStop{reply: r}, r)
}

// Restart stops the service (if running) and starts it again.
func (s *Service) Restart(ctx context.Context, build bool) error {
	r := make(chan error, 1)
	return s.call(ctx, svcRestart{build: build, reply: r}, r)
}

// Kill sends a signal to the service's process group.
func (s *Service) Kill(ctx context.Context, sig string) error {
	r := make(chan error, 1)
	return s.call(ctx, svcKill{sig: sig, reply: r}, r)
}

// Attach opens an interactive attachment to the running process.
func (s *Service) Attach(ctx context.Context, cols, rows int) (*runner.Attachment, error) {
	r := make(chan attachResult, 1)
	if err := s.send(ctx, svcAttach{cols: cols, rows: rows, reply: r}); err != nil {
		return nil, err
	}
	select {
	case res := <-r:
		return res.a, res.err
	case <-s.done:
		return nil, ErrNotFound
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) update(ctx context.Context, def *ProcessDef) error {
	r := make(chan error, 1)
	return s.call(ctx, svcUpdate{def: def, reply: r}, r)
}

// shutdown ends the actor. With stop the process is stopped first;
// otherwise it keeps running under its runner (daemon exit/restart).
func (s *Service) shutdown(ctx context.Context, stop bool) error {
	r := make(chan error, 1)
	err := s.call(ctx, svcShutdown{stop: stop, reply: r}, r)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (a *serviceActor) post(msg any) {
	select {
	case a.h.inbox <- msg:
	case <-a.h.done:
	}
}

func (a *serviceActor) publish() {
	a.hub.set(a.def.Name, a.st)
	a.obs.ServiceChanged(a.def, a.st)
}

func (a *serviceActor) loop(desired bool) {
	defer close(a.h.done)
	a.initialize(desired)
	for msg := range a.h.inbox {
		switch m := msg.(type) {
		case svcStart:
			a.cmdStart(m)
		case svcStop:
			a.cmdStop(m.reply)
		case svcRestart:
			a.cmdRestart(m)
		case svcKill:
			m.reply <- a.cmdKill(m.sig)
		case svcUpdate:
			a.cmdUpdate(m)
		case svcAttach:
			if a.proc == nil {
				m.reply <- attachResult{err: fmt.Errorf("service %s: %w", a.def.Name, ErrNotRunning)}
				continue
			}
			proc := a.proc
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				att, err := proc.Attach(ctx, m.cols, m.rows)
				m.reply <- attachResult{a: att, err: err}
			}()
		case svcShutdown:
			if a.cmdShutdown(m) {
				return
			}
		case evPhase:
			a.onPhase(m)
		case evExited:
			a.onExited(m)
		case evHealth:
			a.onHealth(m)
		case evBackoff:
			if m.gen == a.backoffGen && a.st.Status == StatusBackoff {
				a.backoffTimer = nil
				a.launch()
			}
		case evDeps:
			a.onDeps(m)
		case evStopDeadline:
			a.onStopDeadline(m)
		}
	}
}

// initialize adopts a live run, or applies the recorded outcome of a run
// that ended while no daemon was watching.
func (a *serviceActor) initialize(desired bool) {
	a.desired = desired
	proc, rst, err := a.launcher.Open(a.def.ProcDir)
	switch {
	case err == nil && proc != nil:
		// Adopt the live run.
		a.proc = proc
		a.run = rst.Run
		a.st = ServiceState{Status: statusForPhase(rst.Phase), PID: rst.PID, Run: rst.Run, StartedAt: rst.StartedAt}
		a.desired = true
		a.watch(proc, rst.Run)
		if rst.Phase == runner.PhaseRunning {
			a.startHealth(rst.Run)
		}
		a.publish()
		if rst.Hash != "" && a.def.RuntimeHash != "" && rst.Hash != a.def.RuntimeHash {
			// The config changed while no daemon was watching: apply it.
			a.restartAfterStop = true
			a.stopProc()
		}
		return
	case err == nil && rst.Exited() && !rst.Lost && !rst.Stopped:
		// The run ended on its own while the daemon was away: record it
		// and apply the restart policy as if we had observed the exit.
		a.run = rst.Run
		a.st = ServiceState{Status: StatusStopped, Run: rst.Run, StartedAt: rst.StartedAt}
		a.publish()
		if desired {
			a.onExited(evExited{run: rst.Run, st: rst})
		} else {
			a.recordExit(rst)
			a.publish()
		}
		return
	case err == nil:
		// A run that was stopped on request (e.g. `daemon stop`) or lost
		// (reboot, runner crash): start fresh when desired.
		a.run = rst.Run
		a.st.Run = rst.Run
		a.st.StartedAt = rst.StartedAt
		a.recordExit(rst)
		a.st.Status = StatusStopped
		a.st.Message = ""
	}
	a.publish()
	if desired {
		a.begin()
	}
}

func statusForPhase(phase string) string {
	switch phase {
	case runner.PhaseBuilding:
		return StatusBuilding
	case runner.PhaseRunning:
		return StatusRunning
	case runner.PhaseExited:
		return StatusExited
	default:
		return StatusStarting
	}
}

func (a *serviceActor) cmdStart(m svcStart) {
	a.desired = true
	if m.build {
		a.buildNext = true
	}
	switch a.st.Status {
	case StatusBackoff:
		a.cancelBackoff()
		a.failures = 0
		a.st.Restarts = 0
		a.launch()
	case StatusStopped, StatusExited, StatusFailed:
		a.failures = 0
		a.st.Restarts = 0
		a.begin()
	case StatusStopping:
		// Start after the current stop completes.
		a.restartAfterStop = true
	default:
		// waiting / building / starting / running: already on its way.
		a.publish()
	}
	m.reply <- nil
}

func (a *serviceActor) cmdStop(reply chan error) {
	a.desired = false
	a.restartAfterStop = false
	a.cancelDeps()
	a.cancelBackoff()
	if a.proc == nil {
		if a.st.Status != StatusStopped {
			a.st.Status = StatusStopped
			a.st.Message = ""
			a.st.NextRestartAt = time.Time{}
			a.publish()
		} else {
			a.publish()
		}
		if reply != nil {
			reply <- nil
		}
		return
	}
	if reply != nil {
		a.stopWaiters = append(a.stopWaiters, reply)
	}
	a.stopProc()
}

// stopProc initiates a stop of the current run. Its completion arrives as
// evExited through the watch goroutine.
func (a *serviceActor) stopProc() {
	if a.stopping {
		return
	}
	a.stopping = true
	a.st.Status = StatusStopping
	a.st.Message = ""
	a.publish()
	proc, run, grace := a.proc, a.run, a.grace()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), grace+stopSafety)
		defer cancel()
		_, _ = proc.Stop(ctx, grace)
	}()
	a.stopTimer = time.AfterFunc(grace+stopSafety, func() { a.post(evStopDeadline{run: run}) })
}

func (a *serviceActor) grace() time.Duration {
	if a.def.StopGrace > 0 {
		return a.def.StopGrace
	}
	return defaultStopGrace
}

func (a *serviceActor) cmdRestart(m svcRestart) {
	a.desired = true
	if m.build {
		a.buildNext = true
	}
	a.failures = 0
	a.st.Restarts = 0
	a.cancelBackoff()
	if a.proc != nil {
		a.restartAfterStop = true
		a.stopWaitersForRestart(m.reply)
		a.stopProc()
		return
	}
	a.cancelDeps()
	a.begin()
	m.reply <- nil
}

// stopWaitersForRestart replies to a restart once the stop half is done and
// the start has begun (see onExited).
func (a *serviceActor) stopWaitersForRestart(reply chan error) {
	a.stopWaiters = append(a.stopWaiters, reply)
}

func (a *serviceActor) cmdKill(sigName string) error {
	sig, err := runner.ParseSignal(sigName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if a.proc == nil {
		return fmt.Errorf("service %s: %w", a.def.Name, ErrNotRunning)
	}
	if terminating(sig) {
		// A killed service stays down (like `docker compose kill`).
		a.desired = false
		a.restartAfterStop = false
		a.killedBy = runner.SignalName(sig)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.proc.Signal(ctx, runner.SignalName(sig)); err != nil {
		if errors.Is(err, runner.ErrRunnerGone) {
			// The run just ended; its exit event is on its way.
			return nil
		}
		return fmt.Errorf("service %s: %w: %v", a.def.Name, ErrNotRunning, err)
	}
	return nil
}

func terminating(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT:
		return true
	}
	return false
}

func (a *serviceActor) cmdUpdate(m svcUpdate) {
	old := a.def
	a.def = m.def
	if old.RuntimeHash == m.def.RuntimeHash || old.RuntimeHash == "" {
		a.publish()
		m.reply <- nil
		return
	}
	switch {
	case a.proc != nil && a.desired:
		a.restartAfterStop = true
		a.stopProc()
	case a.st.Status == StatusWaiting:
		a.cancelDeps()
		a.begin()
	default:
		a.publish()
	}
	m.reply <- nil
}

func (a *serviceActor) cmdShutdown(m svcShutdown) bool {
	if m.stop && a.proc != nil {
		// Stop first; finish the shutdown when the exit arrives.
		a.desired = false
		a.restartAfterStop = false
		a.cancelDeps()
		a.cancelBackoff()
		done := make(chan error, 1)
		a.stopWaiters = append(a.stopWaiters, done)
		a.stopProc()
		for a.proc != nil {
			msg := <-a.h.inbox
			switch ev := msg.(type) {
			case evExited:
				a.onExited(ev)
			case evPhase, evHealth, evBackoff, evDeps:
			case evStopDeadline:
				a.onStopDeadline(ev)
			case svcStart:
				ev.reply <- ErrShuttingDown
			case svcStop:
				a.stopWaiters = append(a.stopWaiters, ev.reply)
			case svcRestart:
				ev.reply <- ErrShuttingDown
			case svcKill:
				ev.reply <- a.cmdKill(ev.sig)
			case svcUpdate:
				ev.reply <- nil
			case svcAttach:
				ev.reply <- attachResult{err: ErrShuttingDown}
			case svcShutdown:
				ev.reply <- nil
			}
		}
	}
	a.cancelDeps()
	a.cancelBackoff()
	a.stopHealth()
	if a.runCtx != nil {
		a.runCtx()
	}
	m.reply <- nil
	return true
}

// begin waits for dependencies (if any) and then launches.
func (a *serviceActor) begin() {
	a.cancelDeps()
	a.killedBy = ""
	if len(a.def.Deps) == 0 {
		a.launch()
		return
	}
	a.depsGen++
	gen := a.depsGen
	ctx, cancel := context.WithCancel(context.Background())
	a.depsCancel = cancel
	a.st.Status = StatusWaiting
	a.st.Message = "waiting for " + depList(a.def.Deps)
	a.st.NextRestartAt = time.Time{}
	a.publish()
	deps := a.def.Deps
	go func() {
		var err error
		for _, d := range deps {
			if err = a.hub.waitReady(ctx, d.Name, d.Condition); err != nil {
				break
			}
		}
		if ctx.Err() != nil {
			return
		}
		a.post(evDeps{gen: gen, err: err})
	}()
}

func depList(deps []Dep) string {
	s := ""
	for i, d := range deps {
		if i > 0 {
			s += ", "
		}
		s += d.Name
		if d.Condition == config.ConditionServiceHealthy {
			s += " (healthy)"
		}
	}
	return s
}

func (a *serviceActor) onDeps(m evDeps) {
	if m.gen != a.depsGen || a.st.Status != StatusWaiting {
		return
	}
	a.depsCancel = nil
	if m.err != nil {
		a.st.Status = StatusFailed
		a.st.Message = m.err.Error()
		a.desired = false
		a.publish()
		return
	}
	a.launch()
}

func (a *serviceActor) cancelDeps() {
	if a.depsCancel != nil {
		a.depsCancel()
		a.depsCancel = nil
	}
	a.depsGen++
}

func (a *serviceActor) cancelBackoff() {
	if a.backoffTimer != nil {
		a.backoffTimer.Stop()
		a.backoffTimer = nil
	}
	a.backoffGen++
}

func (a *serviceActor) nextRun() int64 {
	run := logstore.LatestRun(a.def.ProcDir)
	if a.run > run {
		run = a.run
	}
	return run + 1
}

func (a *serviceActor) launch() {
	run := a.nextRun()
	spec := runner.Spec{
		Project:   a.def.Project,
		Kind:      "service",
		Name:      a.def.Name,
		Run:       run,
		Command:   a.def.Command,
		Shell:     a.def.Shell,
		Dir:       a.def.Dir,
		Env:       a.def.Env,
		TTY:       a.def.TTY,
		ProcDir:   a.def.ProcDir,
		Socket:    a.def.Socket,
		StopGrace: a.grace(),
		Hash:      a.def.RuntimeHash,
	}
	if a.buildNext && a.def.Build != nil {
		spec.Build = a.def.Build
	}
	a.buildNext = false
	a.run = run
	a.st = ServiceState{Status: StatusStarting, Run: run, Restarts: a.st.Restarts, StartedAt: time.Now()}
	if spec.Build != nil {
		a.st.Status = StatusBuilding
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	proc, err := a.launcher.Launch(ctx, spec)
	cancel()
	if err != nil {
		a.st.Status = StatusFailed
		a.st.Message = "launch failed: " + err.Error()
		a.st.FinishedAt = time.Now()
		a.publish()
		a.applyRestartPolicy(runner.Status{ExitCode: 127}, 0)
		return
	}
	a.proc = proc
	a.publish()
	a.watch(proc, run)
}

// watch observes the run's phase changes and exit in a goroutine.
func (a *serviceActor) watch(proc Proc, run int64) {
	ctx, cancel := context.WithCancel(context.Background())
	a.runCtx = cancel
	go func() {
		final, err := proc.Watch(ctx, func(st runner.Status) {
			if !st.Exited() {
				a.post(evPhase{run: run, st: st})
			}
		})
		if ctx.Err() != nil {
			return
		}
		a.post(evExited{run: run, st: final, err: err})
	}()
}

func (a *serviceActor) onPhase(m evPhase) {
	if m.run != a.run || a.proc == nil {
		return
	}
	a.st.PID = m.st.PID
	if a.stopping {
		a.publish()
		return
	}
	next := statusForPhase(m.st.Phase)
	if next == a.st.Status {
		return
	}
	a.st.Status = next
	if next == StatusRunning {
		a.st.StartedAt = time.Now()
		a.startHealth(m.run)
	}
	a.publish()
}

func (a *serviceActor) startHealth(run int64) {
	a.stopHealth()
	hc := a.def.Health
	if hc == nil {
		a.st.Health = HealthNone
		return
	}
	c, err := health.New(a.def.Name, health.Config{
		Test:        hc.Test,
		Interval:    hc.Interval,
		Retries:     hc.Retries,
		Timeout:     hc.Timeout,
		StartPeriod: hc.StartPeriod,
		Shell:       a.def.Shell,
		WorkingDir:  a.def.Dir,
		Env:         a.def.Env,
	}, nil)
	if err != nil {
		// An invalid healthcheck must not abort the service.
		a.st.Health = HealthUnhealthy
		a.st.HealthDetail = err.Error()
		return
	}
	a.checker = c
	a.st.Health = HealthStarting
	a.st.HealthDetail = ""
	c.SetOnStateChange(func(s health.State) {
		a.post(evHealth{run: run, state: s, detail: c.LastFailure()})
	})
	c.EnsureStarted(context.Background())
}

func (a *serviceActor) stopHealth() {
	if a.checker != nil {
		c := a.checker
		a.checker = nil
		go c.Stop()
	}
}

func (a *serviceActor) onHealth(m evHealth) {
	if m.run != a.run || a.st.Status != StatusRunning {
		return
	}
	a.st.Health = string(m.state)
	if m.state == health.StateUnhealthy {
		a.st.HealthDetail = m.detail
	} else {
		a.st.HealthDetail = ""
	}
	a.publish()
}

func (a *serviceActor) onStopDeadline(m evStopDeadline) {
	if m.run != a.run || a.proc == nil || !a.stopping {
		return
	}
	// The runner never confirmed the exit. Kill the runner; the watch then
	// resolves the run as lost, which also kills the child's group.
	if st, err := runner.ReadStatus(a.def.ProcDir); err == nil && !st.Exited() && st.RunnerPID > 0 {
		killSession(st.RunnerPID)
	}
}

func (a *serviceActor) recordExit(rst runner.Status) {
	a.st.PID = 0
	a.st.ExitCode = rst.ExitCode
	a.st.FinishedAt = rst.FinishedAt
	if a.st.FinishedAt.IsZero() {
		a.st.FinishedAt = time.Now()
	}
	a.st.Health = HealthNone
	a.st.HealthDetail = ""
	switch {
	case rst.BuildFailed:
		a.st.Status = StatusFailed
		a.st.Message = fmt.Sprintf("build failed (exit code %d)", rst.ExitCode)
	case rst.Lost:
		a.st.Status = StatusExited
		a.st.Message = "runner lost (process was killed)"
	case rst.Error != "":
		a.st.Status = StatusFailed
		a.st.Message = rst.Error
	case rst.Signal != "":
		a.st.Status = StatusExited
		a.st.Message = "killed by " + rst.Signal
	default:
		a.st.Status = StatusExited
		a.st.Message = ""
	}
}

func (a *serviceActor) onExited(m evExited) {
	if m.run != a.run {
		return
	}
	a.stopHealth()
	if a.runCtx != nil {
		a.runCtx()
		a.runCtx = nil
	}
	if a.stopTimer != nil {
		a.stopTimer.Stop()
		a.stopTimer = nil
	}
	started := a.st.StartedAt
	a.proc = nil
	wasStopping := a.stopping
	a.stopping = false
	a.recordExit(m.st)
	if m.err != nil && a.st.Message == "" {
		a.st.Message = m.err.Error()
	}

	waiters := a.stopWaiters
	a.stopWaiters = nil
	if a.restartAfterStop {
		a.restartAfterStop = false
		a.begin()
		for _, w := range waiters {
			w <- nil
		}
		return
	}
	if wasStopping || !a.desired {
		a.st.Status = StatusStopped
		if a.killedBy != "" {
			a.st.Message = "killed by " + a.killedBy
		} else {
			a.st.Message = ""
		}
		a.publish()
		for _, w := range waiters {
			w <- nil
		}
		return
	}
	a.publish()
	for _, w := range waiters {
		w <- nil
	}
	lasted := time.Duration(0)
	if !started.IsZero() {
		lasted = a.st.FinishedAt.Sub(started)
	}
	a.applyRestartPolicy(m.st, lasted)
}

func (a *serviceActor) applyRestartPolicy(rst runner.Status, lasted time.Duration) {
	if !a.desired || rst.BuildFailed {
		return
	}
	failed := rst.ExitCode != 0 || rst.Lost
	switch a.def.Restart {
	case config.RestartAlways:
	case config.RestartOnFailure:
		if !failed {
			return
		}
	default:
		return
	}
	if lasted >= stableRun {
		a.failures = 0
	}
	if a.def.Restart == config.RestartOnFailure && a.failures >= maxOnFailureRetries {
		a.st.Status = StatusFailed
		a.st.Message = fmt.Sprintf("gave up after %d failed restarts", a.failures)
		a.desired = false
		a.publish()
		return
	}
	delay := backoffBase << min(a.failures, 6)
	if delay > backoffMax {
		delay = backoffMax
	}
	delay = delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
	a.failures++
	a.st.Restarts++
	a.backoffGen++
	gen := a.backoffGen
	a.st.Status = StatusBackoff
	a.st.NextRestartAt = time.Now().Add(delay)
	a.st.Message = fmt.Sprintf("restarting in %s", delay.Round(100*time.Millisecond))
	a.publish()
	a.backoffTimer = time.AfterFunc(delay, func() { a.post(evBackoff{gen: gen}) })
}
