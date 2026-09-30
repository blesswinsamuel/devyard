package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blesswinsamuel/devyard/internal/logstore"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// Task is the actor handle of one task.
type Task struct {
	name  string
	inbox chan any
	done  chan struct{}
}

// Name returns the task name.
func (t *Task) Name() string { return t.name }

type taskRun struct {
	args  []string
	reply chan runReply
}
type runReply struct {
	run int64
	err error
}

type taskActor struct {
	h        *Task
	def      *ProcessDef
	launcher Launcher
	hub      *hub
	obs      Observer

	st         TaskState
	proc       Proc
	run        int64
	runCtx     context.CancelFunc
	stopping   bool
	waiters    []chan error
	depsCancel context.CancelFunc
	depsGen    int
	pendingRun []string
	stopTimer  *time.Timer
}

func startTask(def *ProcessDef, launcher Launcher, h *hub, obs Observer) *Task {
	t := &Task{name: def.Name, inbox: make(chan any, 64), done: make(chan struct{})}
	a := &taskActor{h: t, def: def, launcher: launcher, hub: h, obs: obs, st: TaskState{Status: StatusIdle}}
	go a.loop()
	return t
}

func (t *Task) send(ctx context.Context, msg any) error {
	select {
	case t.inbox <- msg:
		return nil
	case <-t.done:
		return fmt.Errorf("task %s: %w", t.name, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Task) call(ctx context.Context, msg any, reply chan error) error {
	if err := t.send(ctx, msg); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-t.done:
		return fmt.Errorf("task %s: %w", t.name, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run starts a new run of the task and returns its run number. It fails
// with ErrAlreadyRunning while a run is in progress.
func (t *Task) Run(ctx context.Context, args []string) (int64, error) {
	r := make(chan runReply, 1)
	if err := t.send(ctx, taskRun{args: args, reply: r}); err != nil {
		return 0, err
	}
	select {
	case res := <-r:
		return res.run, res.err
	case <-t.done:
		return 0, ErrNotFound
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Stop stops the running task and returns once it has exited.
func (t *Task) Stop(ctx context.Context) error {
	r := make(chan error, 1)
	return t.call(ctx, svcStop{reply: r}, r)
}

// Kill signals the running task's process group.
func (t *Task) Kill(ctx context.Context, sig string) error {
	r := make(chan error, 1)
	return t.call(ctx, svcKill{sig: sig, reply: r}, r)
}

// Attach opens an interactive attachment to the running task.
func (t *Task) Attach(ctx context.Context, cols, rows int) (*runner.Attachment, error) {
	r := make(chan attachResult, 1)
	if err := t.send(ctx, svcAttach{cols: cols, rows: rows, reply: r}); err != nil {
		return nil, err
	}
	select {
	case res := <-r:
		return res.a, res.err
	case <-t.done:
		return nil, ErrNotFound
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *Task) update(ctx context.Context, def *ProcessDef) error {
	r := make(chan error, 1)
	return t.call(ctx, svcUpdate{def: def, reply: r}, r)
}

func (t *Task) shutdown(ctx context.Context, stop bool) error {
	r := make(chan error, 1)
	err := t.call(ctx, svcShutdown{stop: stop, reply: r}, r)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (a *taskActor) post(msg any) {
	select {
	case a.h.inbox <- msg:
	case <-a.h.done:
	}
}

func (a *taskActor) publish() { a.obs.TaskChanged(a.def, a.st) }

func (a *taskActor) active() bool {
	switch a.st.Status {
	case StatusWaiting, StatusRunning, StatusStopping, StatusStarting:
		return true
	}
	return false
}

func (a *taskActor) loop() {
	defer close(a.h.done)
	a.initialize()
	for msg := range a.h.inbox {
		switch m := msg.(type) {
		case taskRun:
			m.reply <- a.cmdRun(m.args)
		case svcStop:
			a.cmdStop(m.reply)
		case svcKill:
			m.reply <- a.cmdKill(m.sig)
		case svcUpdate:
			a.def = m.def
			m.reply <- nil
		case svcAttach:
			if a.proc == nil {
				m.reply <- attachResult{err: fmt.Errorf("task %s: %w", a.def.Name, ErrNotRunning)}
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
			a.cmdShutdown(m)
			return
		case evPhase:
			if m.run == a.run && a.proc != nil && m.st.PID != 0 && a.st.PID != m.st.PID {
				a.st.PID = m.st.PID
				if !a.stopping {
					a.st.Status = StatusRunning
				}
				a.publish()
			}
		case evExited:
			a.onExited(m)
		case evDeps:
			a.onDeps(m)
		case evStopDeadline:
			if m.run == a.run && a.proc != nil && a.stopping {
				if st, err := runner.ReadStatus(a.def.ProcDir); err == nil && !st.Exited() && st.RunnerPID > 0 {
					killSession(st.RunnerPID)
				}
			}
		}
	}
}

func (a *taskActor) initialize() {
	proc, rst, err := a.launcher.Open(a.def.ProcDir)
	if err != nil {
		a.publish()
		return
	}
	a.run = rst.Run
	if proc != nil {
		a.proc = proc
		a.st = TaskState{Status: StatusRunning, PID: rst.PID, Run: rst.Run, StartedAt: rst.StartedAt}
		a.watch(proc, rst.Run)
		a.publish()
		return
	}
	a.st = TaskState{Run: rst.Run, StartedAt: rst.StartedAt}
	a.recordExit(rst)
	a.publish()
}

func (a *taskActor) cmdRun(args []string) runReply {
	if a.active() {
		return runReply{err: fmt.Errorf("task %s: %w", a.def.Name, ErrAlreadyRunning)}
	}
	run := logstore.LatestRun(a.def.ProcDir)
	if a.run > run {
		run = a.run
	}
	run++
	a.run = run
	a.st = TaskState{Status: StatusWaiting, Run: run, Args: args, StartedAt: time.Now()}
	a.pendingRun = args
	if len(a.def.Deps) == 0 {
		a.launch(run, args)
		return runReply{run: run}
	}
	a.depsGen++
	gen := a.depsGen
	ctx, cancel := context.WithCancel(context.Background())
	a.depsCancel = cancel
	a.st.Message = "waiting for " + depList(a.def.Deps)
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
	return runReply{run: run}
}

func (a *taskActor) onDeps(m evDeps) {
	if m.gen != a.depsGen || a.st.Status != StatusWaiting {
		return
	}
	a.depsCancel = nil
	if m.err != nil {
		a.st.Status = StatusFailed
		a.st.Message = m.err.Error()
		a.st.FinishedAt = time.Now()
		a.publish()
		return
	}
	a.launch(a.run, a.pendingRun)
}

func (a *taskActor) launch(run int64, args []string) {
	command := a.def.Command
	if len(args) > 0 {
		command += " " + shellJoin(args)
	}
	spec := runner.Spec{
		Project:   a.def.Project,
		Kind:      "task",
		Name:      a.def.Name,
		Run:       run,
		Command:   command,
		Shell:     a.def.Shell,
		Dir:       a.def.Dir,
		Env:       a.def.Env,
		TTY:       a.def.TTY,
		ProcDir:   a.def.ProcDir,
		Socket:    a.def.Socket,
		StopGrace: defaultStopGrace,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	proc, err := a.launcher.Launch(ctx, spec)
	cancel()
	if err != nil {
		a.st.Status = StatusFailed
		a.st.Message = "launch failed: " + err.Error()
		a.st.FinishedAt = time.Now()
		a.publish()
		return
	}
	a.proc = proc
	a.st.Status = StatusRunning
	a.st.Message = ""
	a.publish()
	a.watch(proc, run)
}

// shellJoin quotes args for sh -c.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func (a *taskActor) watch(proc Proc, run int64) {
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

func (a *taskActor) recordExit(rst runner.Status) {
	a.st.PID = 0
	a.st.ExitCode = rst.ExitCode
	a.st.FinishedAt = rst.FinishedAt
	if a.st.FinishedAt.IsZero() {
		a.st.FinishedAt = time.Now()
	}
	a.st.Status = StatusExited
	a.st.Message = ""
	switch {
	case rst.Lost:
		a.st.Message = "runner lost (process was killed)"
	case rst.Error != "":
		a.st.Status = StatusFailed
		a.st.Message = rst.Error
	case rst.Stopped:
		a.st.Message = "stopped"
	case rst.Signal != "":
		a.st.Message = "killed by " + rst.Signal
	}
}

func (a *taskActor) onExited(m evExited) {
	if m.run != a.run || a.proc == nil {
		return
	}
	if a.runCtx != nil {
		a.runCtx()
		a.runCtx = nil
	}
	if a.stopTimer != nil {
		a.stopTimer.Stop()
		a.stopTimer = nil
	}
	a.proc = nil
	a.stopping = false
	a.recordExit(m.st)
	a.publish()
	for _, w := range a.waiters {
		w <- nil
	}
	a.waiters = nil
}

func (a *taskActor) cmdStop(reply chan error) {
	if a.depsCancel != nil {
		a.depsCancel()
		a.depsCancel = nil
		a.depsGen++
	}
	if a.proc == nil {
		if a.st.Status == StatusWaiting {
			a.st.Status = StatusExited
			a.st.Message = "stopped"
			a.st.FinishedAt = time.Now()
			a.publish()
		}
		if reply != nil {
			reply <- nil
		}
		return
	}
	if reply != nil {
		a.waiters = append(a.waiters, reply)
	}
	if a.stopping {
		return
	}
	a.stopping = true
	a.st.Status = StatusStopping
	a.publish()
	proc, run := a.proc, a.run
	go stopWithRetry(proc, defaultStopGrace)
	a.stopTimer = time.AfterFunc(defaultStopGrace+stopSafety, func() { a.post(evStopDeadline{run: run}) })
}

func (a *taskActor) cmdKill(sigName string) error {
	sig, err := runner.ParseSignal(sigName)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if a.proc == nil {
		return fmt.Errorf("task %s: %w", a.def.Name, ErrNotRunning)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.proc.Signal(ctx, runner.SignalName(sig)); err != nil {
		if errors.Is(err, runner.ErrRunnerGone) {
			return nil
		}
		return fmt.Errorf("task %s: %w: %v", a.def.Name, ErrNotRunning, err)
	}
	return nil
}

func (a *taskActor) cmdShutdown(m svcShutdown) {
	if m.stop && a.proc != nil {
		done := make(chan error, 1)
		a.cmdStop(done)
		for a.proc != nil {
			msg := <-a.h.inbox
			switch ev := msg.(type) {
			case evExited:
				a.onExited(ev)
			case evStopDeadline:
				if st, err := runner.ReadStatus(a.def.ProcDir); err == nil && !st.Exited() && st.RunnerPID > 0 {
					killSession(st.RunnerPID)
				}
			case taskRun:
				ev.reply <- runReply{err: ErrShuttingDown}
			case svcStop:
				a.waiters = append(a.waiters, ev.reply)
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
	if a.depsCancel != nil {
		a.depsCancel()
	}
	if a.runCtx != nil {
		a.runCtx()
	}
	m.reply <- nil
}
