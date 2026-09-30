package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/paths"
	"github.com/blesswinsamuel/devyard/internal/runner"
)

// Project is the actor handle of one project. Lifecycle commands are
// serialized through its mailbox; per-service commands that must stay
// responsive (stop, restart, kill, attach) go straight to the service actors
// found in the project's immutable view.
type Project struct {
	id    string
	inbox chan any
	done  chan struct{}
	view  atomic.Pointer[View]

	statusMu   sync.Mutex
	lastStatus ProjectState
	// stopping is set while a project-wide stop is in progress, so the
	// project reports stopping until every service has been handled.
	stopping atomic.Bool
	obs      Observer
	hub      *hub
}

// View is an immutable snapshot of a project's structure. A new View is
// swapped in whenever the structure changes; readers never see partial
// updates.
type View struct {
	Reg         Registration
	LoadErr     string
	Order       []string
	ServiceDefs map[string]*ProcessDef
	TaskDefs    map[string]*ProcessDef
	Services    map[string]*Service
	Tasks       map[string]*Task
	// DefaultService is the proxy's project-level default service.
	DefaultService string
	Warnings       []string
	// TerminalEnv is the environment for interactive shells in the
	// project (launch environment plus env file).
	TerminalEnv []string
}

// ID returns the project id.
func (p *Project) ID() string { return p.id }

// View returns the current snapshot.
func (p *Project) View() *View { return p.view.Load() }

type projStart struct {
	services []string
	build    bool
	reply    chan error
}
type projStop struct{ reply chan error }
type projRestart struct {
	build bool
	reply chan error
}
type projReload struct {
	env    []string
	setEnv bool
	reply  chan error
}
type projReconfigure struct {
	configPath string
	envFile    string
	env        []string
	reply      chan error
}
type projRemove struct{ reply chan error }
type projCheckConfig struct{}
type projStartService struct {
	name  string
	build bool
	reply chan error
}
type projRunTask struct {
	name  string
	args  []string
	reply chan runReply
}

type projectActor struct {
	p        *Project
	dirs     paths.Dirs
	launcher Launcher
	obs      Observer

	reg      Registration
	ld       *loaded
	loadErr  error
	services map[string]*Service
	tasks    map[string]*Task
	// configMissing is set while the config file does not exist.
	configMissing bool
}

// configCheckInterval is how often a project checks that its config file
// still exists.
const configCheckInterval = 2 * time.Second

// projectObserver forwards entity events and recomputes the project status.
type projectObserver struct {
	p *Project
}

func (o projectObserver) ProjectChanged(st ProjectState) { o.p.obs.ProjectChanged(st) }
func (o projectObserver) ProjectRemoved(id string)       { o.p.obs.ProjectRemoved(id) }
func (o projectObserver) ServiceChanged(def *ProcessDef, st ServiceState) {
	o.p.obs.ServiceChanged(def, st)
	o.p.recompute()
}
func (o projectObserver) ServiceRemoved(project, name string) {
	o.p.obs.ServiceRemoved(project, name)
	o.p.recompute()
}
func (o projectObserver) TaskChanged(def *ProcessDef, st TaskState) { o.p.obs.TaskChanged(def, st) }
func (o projectObserver) TaskRemoved(project, name string)          { o.p.obs.TaskRemoved(project, name) }

func newProject(dirs paths.Dirs, reg Registration, launcher Launcher, obs Observer) *Project {
	p := &Project{id: reg.ID, inbox: make(chan any, 16), done: make(chan struct{}), obs: obs, hub: newHub()}
	a := &projectActor{
		p:        p,
		dirs:     dirs,
		launcher: launcher,
		obs:      projectObserver{p: p},
		reg:      reg,
		services: map[string]*Service{},
		tasks:    map[string]*Task{},
	}
	a.initialize()
	go a.loop()
	go func() {
		t := time.NewTicker(configCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-t.C:
				select {
				case p.inbox <- projCheckConfig{}:
				default: // busy; check next tick
				}
			}
		}
	}()
	return p
}

// --- public commands ---------------------------------------------------------

func (p *Project) send(ctx context.Context, msg any) error {
	select {
	case p.inbox <- msg:
		return nil
	case <-p.done:
		return fmt.Errorf("project %s: %w", p.id, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Project) call(ctx context.Context, msg any, reply chan error) error {
	if err := p.send(ctx, msg); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-p.done:
		return fmt.Errorf("project %s: %w", p.id, ErrNotFound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start starts every service (services empty) or the named services plus
// their dependencies.
func (p *Project) Start(ctx context.Context, services []string, build bool) error {
	r := make(chan error, 1)
	return p.call(ctx, projStart{services: services, build: build, reply: r}, r)
}

// Stop stops all services and tasks and marks the project stopped (it will
// not autostart).
func (p *Project) Stop(ctx context.Context) error {
	r := make(chan error, 1)
	return p.call(ctx, projStop{reply: r}, r)
}

// Restart stops and starts the wanted services (rebuilding first when
// build is set).
func (p *Project) Restart(ctx context.Context, build bool) error {
	r := make(chan error, 1)
	return p.call(ctx, projRestart{build: build, reply: r}, r)
}

// Reload re-reads the config and reconciles services. When setEnv is true
// the stored launch environment is replaced by env first.
func (p *Project) Reload(ctx context.Context, env []string, setEnv bool) error {
	r := make(chan error, 1)
	return p.call(ctx, projReload{env: env, setEnv: setEnv, reply: r}, r)
}

func (p *Project) reconfigure(ctx context.Context, configPath, envFile string, env []string) error {
	r := make(chan error, 1)
	return p.call(ctx, projReconfigure{configPath: configPath, envFile: envFile, env: env, reply: r}, r)
}

func (p *Project) remove(ctx context.Context) error {
	r := make(chan error, 1)
	return p.call(ctx, projRemove{reply: r}, r)
}

func (p *Project) shutdown(ctx context.Context, stop bool) error {
	r := make(chan error, 1)
	err := p.call(ctx, svcShutdown{stop: stop, reply: r}, r)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// StartService starts one service and its dependencies.
func (p *Project) StartService(ctx context.Context, name string, build bool) error {
	r := make(chan error, 1)
	return p.call(ctx, projStartService{name: name, build: build, reply: r}, r)
}

// RunTask starts a task run (starting its service dependencies first).
func (p *Project) RunTask(ctx context.Context, name string, args []string) (int64, error) {
	r := make(chan runReply, 1)
	if err := p.send(ctx, projRunTask{name: name, args: args, reply: r}); err != nil {
		return 0, err
	}
	select {
	case res := <-r:
		return res.run, res.err
	case <-p.done:
		return 0, ErrNotFound
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Service returns the actor of a service.
func (p *Project) Service(name string) (*Service, error) {
	if s, ok := p.View().Services[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("service %q in project %s: %w", name, p.id, ErrNotFound)
}

// Task returns the actor of a task.
func (p *Project) Task(name string) (*Task, error) {
	if t, ok := p.View().Tasks[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("task %q in project %s: %w", name, p.id, ErrNotFound)
}

// ServiceStates returns the latest state of every service.
func (p *Project) ServiceStates() map[string]ServiceState {
	return p.hub.snapshot()
}

// --- status ------------------------------------------------------------------

// recompute derives the project status from its services and publishes it
// when it changed. Safe for concurrent use.
func (p *Project) recompute() {
	// Compute and publish under one lock: a computation that read older
	// inputs must never publish after a newer one.
	p.statusMu.Lock()
	defer p.statusMu.Unlock()
	v := p.View()
	if v == nil {
		return
	}
	states := p.hub.snapshot()
	st := ProjectState{
		ID:             p.id,
		ConfigPath:     v.Reg.ConfigPath,
		EnvFile:        v.Reg.EnvFile,
		Desired:        v.Reg.Desired,
		Error:          v.LoadErr,
		ServicesTotal:  len(v.ServiceDefs),
		DefaultService: v.DefaultService,
	}
	if st.ServicesTotal == 0 {
		st.ServicesTotal = len(states)
	}
	wanted := func(name string) bool {
		switch v.Reg.Desired {
		case DesiredRunning:
			return true
		case DesiredPartial:
			return slices.Contains(v.Reg.Selected, name)
		}
		return false
	}
	stopping, starting, degraded, active, wantedCount := false, false, false, 0, 0
	for name, s := range states {
		if s.Status == StatusRunning {
			st.ServicesActive++
		}
		if s.Active() || s.Status == StatusWaiting || s.Status == StatusBackoff {
			active++
		}
		if s.Status == StatusStopping {
			stopping = true
		}
		if !wanted(name) {
			continue
		}
		wantedCount++
		switch s.Status {
		case StatusWaiting, StatusStarting, StatusBuilding, StatusBackoff:
			starting = true
		case StatusFailed:
			degraded = true
		case StatusExited:
			if s.ExitCode != 0 {
				degraded = true
			}
		case StatusRunning:
			switch s.Health {
			case HealthUnhealthy:
				degraded = true
			case HealthStarting:
				starting = true
			}
		}
	}
	switch {
	case v.LoadErr != "":
		st.Status = ProjectError
	case stopping || p.stopping.Load():
		st.Status = ProjectStopping
	case v.Reg.Desired == DesiredStopped || wantedCount == 0:
		if active > 0 {
			st.Status = ProjectStopping
		} else {
			st.Status = ProjectStopped
		}
	case degraded:
		st.Status = ProjectDegraded
	case starting:
		st.Status = ProjectStarting
	default:
		st.Status = ProjectRunning
	}
	cmp := p.lastStatus
	cmp.UpdatedAt = time.Time{}
	if cmp == st {
		return
	}
	st.UpdatedAt = time.Now()
	p.lastStatus = st
	p.obs.ProjectChanged(st)
}

// State returns the last published project state.
func (p *Project) State() ProjectState {
	p.statusMu.Lock()
	defer p.statusMu.Unlock()
	return p.lastStatus
}

// --- actor -------------------------------------------------------------------

func (a *projectActor) publishView() {
	v := &View{
		Reg:         a.reg,
		ServiceDefs: map[string]*ProcessDef{},
		TaskDefs:    map[string]*ProcessDef{},
		Services:    make(map[string]*Service, len(a.services)),
		Tasks:       make(map[string]*Task, len(a.tasks)),
	}
	v.Reg.Env = nil // never expose the captured environment
	v.TerminalEnv = config.ChildEnv(a.reg.Env, nil, nil)
	v.Reg.Selected = append([]string(nil), a.reg.Selected...)
	if a.loadErr != nil {
		v.LoadErr = a.loadErr.Error()
	}
	if a.ld != nil {
		v.Order = append([]string(nil), a.ld.order...)
		for k, d := range a.ld.services {
			v.ServiceDefs[k] = d
		}
		for k, d := range a.ld.tasks {
			v.TaskDefs[k] = d
		}
		if a.ld.file.Proxy != nil {
			v.DefaultService = a.ld.file.Proxy.DefaultService
		}
		v.Warnings = a.ld.warnings
		v.TerminalEnv = config.ChildEnv(a.reg.Env, a.ld.dotenv, nil)
		if a.ld.envFile != "" && v.Reg.EnvFile == "" {
			v.Reg.EnvFile = a.ld.envFile
		}
	}
	for k, s := range a.services {
		v.Services[k] = s
	}
	for k, t := range a.tasks {
		v.Tasks[k] = t
	}
	a.p.view.Store(v)
	a.p.recompute()
}

func (a *projectActor) wanted(name string) bool {
	switch a.reg.Desired {
	case DesiredRunning:
		return true
	case DesiredPartial:
		return slices.Contains(a.reg.Selected, name)
	}
	return false
}

func (a *projectActor) initialize() {
	a.ld, a.loadErr = loadProject(a.dirs, &a.reg)
	if a.loadErr != nil {
		a.adoptOrphans()
		a.publishView()
		return
	}
	for _, name := range a.ld.order {
		a.p.hub.set(name, ServiceState{Status: StatusStopped})
	}
	a.publishView()
	for _, name := range a.ld.order {
		a.services[name] = startService(a.ld.services[name], a.launcher, a.p.hub, a.obs, a.wanted(name))
	}
	for name, def := range a.ld.tasks {
		a.tasks[name] = startTask(def, a.launcher, a.p.hub, a.obs)
	}
	a.stopUnknownRuns()
	a.publishView()
}

// adoptOrphans creates placeholder actors for live runs when the config
// cannot be loaded, so they remain visible and stoppable.
func (a *projectActor) adoptOrphans() {
	pd, err := a.dirs.Project(a.reg.ID)
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(filepath.Join(pd.Root, "procs"))
	for _, e := range entries {
		kind, name, ok := strings.Cut(e.Name(), "-")
		if !ok || kind != "service" {
			continue
		}
		dir := filepath.Join(pd.Root, "procs", e.Name())
		st, err := runner.ReadStatus(dir)
		if err != nil || st.Exited() || !runner.Alive(st.RunnerPID) {
			continue
		}
		def := &ProcessDef{Project: a.reg.ID, Kind: "service", Name: name, ProcDir: dir, Socket: st.Socket, RuntimeHash: st.Hash}
		a.p.hub.set(name, ServiceState{Status: StatusStopped})
		a.services[name] = startService(def, a.launcher, a.p.hub, a.obs, true)
	}
}

// stopUnknownRuns stops live runs of services that are no longer in the
// config (e.g. removed while the daemon was down).
func (a *projectActor) stopUnknownRuns() {
	pd, err := a.dirs.Project(a.reg.ID)
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(filepath.Join(pd.Root, "procs"))
	for _, e := range entries {
		kind, name, ok := strings.Cut(e.Name(), "-")
		if !ok {
			continue
		}
		if (kind == "service" && a.ld.services[name] != nil) || (kind == "task" && a.ld.tasks[name] != nil) {
			continue
		}
		dir := filepath.Join(pd.Root, "procs", e.Name())
		proc, st, err := a.launcher.Open(dir)
		if err != nil || proc == nil || st.Exited() {
			continue
		}
		go stopWithRetry(proc, defaultStopGrace)
	}
}

func (a *projectActor) loop() {
	defer close(a.p.done)
	for msg := range a.p.inbox {
		switch m := msg.(type) {
		case projStart:
			m.reply <- a.cmdStart(m.services, m.build)
		case projStop:
			m.reply <- a.cmdStop()
		case projRestart:
			m.reply <- a.cmdRestart(m.build)
		case projReload:
			if m.setEnv {
				a.reg.Env = CleanEnv(m.env)
			}
			m.reply <- a.reload()
		case projReconfigure:
			m.reply <- a.cmdReconfigure(m)
		case projStartService:
			m.reply <- a.cmdStartService(m.name, m.build)
		case projRunTask:
			run, err := a.cmdRunTask(m.name, m.args)
			m.reply <- runReply{run: run, err: err}
		case projCheckConfig:
			a.checkConfig()
		case projRemove:
			err := a.cmdRemove()
			m.reply <- err
			if err == nil {
				return
			}
		case svcShutdown:
			a.shutdownAll(m.stop)
			m.reply <- nil
			return
		}
	}
}

// checkConfig reports a config file that disappeared (without touching
// running services) and reloads once it is back.
func (a *projectActor) checkConfig() {
	_, err := os.Stat(a.reg.ConfigPath)
	switch {
	case err != nil && !a.configMissing:
		a.configMissing = true
		a.loadErr = fmt.Errorf("config file %s not found", a.reg.ConfigPath)
		a.publishView()
	case err == nil && a.configMissing:
		a.configMissing = false
		_ = a.reload()
	}
}

func (a *projectActor) save() error {
	return saveRegistration(a.dirs, &a.reg)
}

func (a *projectActor) requireConfig() error {
	if a.ld != nil && a.loadErr == nil {
		return nil
	}
	if err := a.reload(); err != nil {
		return err
	}
	return nil
}

func (a *projectActor) cmdStart(names []string, build bool) error {
	if err := a.requireConfig(); err != nil {
		return err
	}
	var targets []string
	if len(names) == 0 {
		a.reg.Desired = DesiredRunning
		a.reg.Selected = nil
		targets = a.ld.order
	} else {
		closure, err := a.ld.closure(names)
		if err != nil {
			return err
		}
		targets = closure
		if a.reg.Desired != DesiredRunning {
			a.reg.Desired = DesiredPartial
			a.reg.Selected = union(a.reg.Selected, closure)
		}
	}
	if err := a.save(); err != nil {
		return err
	}
	a.publishView()
	return a.startServices(targets, build, names)
}

// startServices starts targets (in dependency order).
func (a *projectActor) startServices(targets []string, build bool, buildOnly []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var errs []error
	for _, name := range targets {
		svc := a.services[name]
		if svc == nil {
			continue
		}
		b := build && (len(buildOnly) == 0 || slices.Contains(buildOnly, name))
		if err := svc.Start(ctx, b); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func (a *projectActor) cmdStop() error {
	// Report stopping (not stopped) until every service has been handled.
	a.p.stopping.Store(true)
	a.reg.Desired = DesiredStopped
	a.reg.Selected = nil
	if err := a.save(); err != nil {
		a.p.stopping.Store(false)
		return err
	}
	a.publishView()
	return a.stopEverything()
}

// stopEverything stops running tasks, then services in reverse dependency
// order (dependents before their dependencies), each level concurrently.
func (a *projectActor) stopEverything() error {
	a.p.stopping.Store(true)
	a.p.recompute()
	defer func() {
		a.p.stopping.Store(false)
		a.p.recompute()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	for _, t := range a.tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = t.Stop(ctx)
		}()
	}
	wg.Wait()
	var errs []error
	var mu sync.Mutex
	for _, level := range a.stopLevels() {
		for _, name := range level {
			svc := a.services[name]
			if svc == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := svc.Stop(ctx); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
	}
	return errors.Join(errs...)
}

// stopLevels groups services by dependency depth, deepest dependents first.
func (a *projectActor) stopLevels() [][]string {
	depth := map[string]int{}
	var order []string
	if a.ld != nil {
		order = a.ld.order
	}
	for name := range a.services {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	maxDepth := 0
	for _, name := range order {
		d := 0
		if a.ld != nil {
			if def := a.ld.services[name]; def != nil {
				for _, dep := range def.Deps {
					d = max(d, depth[dep.Name]+1)
				}
			}
		}
		depth[name] = d
		maxDepth = max(maxDepth, d)
	}
	levels := make([][]string, maxDepth+1)
	for _, name := range order {
		levels[maxDepth-depth[name]] = append(levels[maxDepth-depth[name]], name)
	}
	return levels
}

func (a *projectActor) cmdRestart(build bool) error {
	if err := a.requireConfig(); err != nil {
		return err
	}
	if err := a.stopEverything(); err != nil {
		return err
	}
	if a.reg.Desired == DesiredStopped {
		a.reg.Desired = DesiredRunning
		a.reg.Selected = nil
		if err := a.save(); err != nil {
			return err
		}
		a.publishView()
	}
	var targets []string
	for _, name := range a.ld.order {
		if a.wanted(name) {
			targets = append(targets, name)
		}
	}
	return a.startServices(targets, build, nil)
}

func (a *projectActor) cmdReconfigure(m projReconfigure) error {
	if m.configPath != a.reg.ConfigPath {
		if _, err := os.Stat(a.reg.ConfigPath); err == nil {
			return fmt.Errorf("project %q is already registered from %s: %w (set a different `name:` in %s)", a.reg.ID, a.reg.ConfigPath, ErrAlreadyExists, m.configPath)
		}
		// The old config no longer exists: the project moved.
		a.reg.ConfigPath = m.configPath
	}
	a.reg.EnvFile = m.envFile
	if m.env != nil {
		a.reg.Env = CleanEnv(m.env)
	}
	return a.reload()
}

// reload re-reads the config and reconciles actors with it. On failure the
// project keeps its current actors (running services are not touched) and
// reports the error.
func (a *projectActor) reload() error {
	if err := a.save(); err != nil {
		return err
	}
	ld, err := loadProject(a.dirs, &a.reg)
	if err != nil {
		a.loadErr = err
		a.publishView()
		return fmt.Errorf("project %s: %w: %v", a.reg.ID, ErrConfig, err)
	}
	a.loadErr = nil
	a.ld = ld
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Removed services and tasks are stopped and forgotten.
	for name, svc := range a.services {
		if _, ok := ld.services[name]; !ok {
			_ = svc.shutdown(ctx, true)
			delete(a.services, name)
			a.p.hub.remove(name)
			a.obs.ServiceRemoved(a.reg.ID, name)
		}
	}
	for name, t := range a.tasks {
		if _, ok := ld.tasks[name]; !ok {
			_ = t.shutdown(ctx, true)
			delete(a.tasks, name)
			a.obs.TaskRemoved(a.reg.ID, name)
		}
	}
	a.publishView()
	// Changed definitions update in place (the actor restarts the process
	// only when runtime-relevant fields changed).
	var added []string
	for _, name := range ld.order {
		def := ld.services[name]
		if svc, ok := a.services[name]; ok {
			_ = svc.update(ctx, def)
			continue
		}
		added = append(added, name)
	}
	for _, name := range added {
		a.p.hub.set(name, ServiceState{Status: StatusStopped})
	}
	for _, name := range added {
		a.services[name] = startService(ld.services[name], a.launcher, a.p.hub, a.obs, a.wanted(name))
	}
	for name, def := range ld.tasks {
		if t, ok := a.tasks[name]; ok {
			_ = t.update(ctx, def)
			continue
		}
		a.tasks[name] = startTask(def, a.launcher, a.p.hub, a.obs)
	}
	if a.reg.Desired == DesiredPartial {
		var kept []string
		for _, s := range a.reg.Selected {
			if _, ok := ld.services[s]; ok {
				kept = append(kept, s)
			}
		}
		a.reg.Selected = kept
		_ = a.save()
	}
	a.publishView()
	return nil
}

func (a *projectActor) cmdStartService(name string, build bool) error {
	if err := a.requireConfig(); err != nil {
		return err
	}
	return a.cmdStart([]string{name}, build)
}

func (a *projectActor) cmdRunTask(name string, args []string) (int64, error) {
	if err := a.requireConfig(); err != nil {
		return 0, err
	}
	t, ok := a.tasks[name]
	def := a.ld.tasks[name]
	if !ok || def == nil {
		return 0, fmt.Errorf("task %q in project %s: %w", name, a.reg.ID, ErrNotFound)
	}
	if len(def.Deps) > 0 {
		var names []string
		for _, d := range def.Deps {
			names = append(names, d.Name)
		}
		if err := a.cmdStart(names, false); err != nil {
			return 0, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.Run(ctx, args)
}

func (a *projectActor) cmdRemove() error {
	a.shutdownAll(true)
	for name := range a.services {
		a.obs.ServiceRemoved(a.reg.ID, name)
	}
	for name := range a.tasks {
		a.obs.TaskRemoved(a.reg.ID, name)
	}
	if err := deleteProjectDir(a.dirs, a.reg.ID); err != nil {
		return fmt.Errorf("project %s: remove state: %w", a.reg.ID, err)
	}
	a.p.obs.ProjectRemoved(a.reg.ID)
	return nil
}

func (a *projectActor) shutdownAll(stop bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if stop {
		_ = a.stopEverything()
	}
	var wg sync.WaitGroup
	for _, s := range a.services {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.shutdown(ctx, false) }()
	}
	for _, t := range a.tasks {
		wg.Add(1)
		go func() { defer wg.Done(); _ = t.shutdown(ctx, false) }()
	}
	wg.Wait()
}
