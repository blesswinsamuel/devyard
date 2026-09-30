package harness

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// State is the materialized daemon state (from GetState or Watch). Entity
// messages are never mutated after decoding, so copies share them.
type State struct {
	Revision uint64
	Daemon   *v1.DaemonInfo
	Projects map[string]*v1.Project
	Services map[string]*v1.Service // Key(project, name)
	Tasks    map[string]*v1.Task    // Key(project, name)
	Git      map[string]*v1.GitStatus
}

// Key identifies a service or task.
func Key(project, name string) string { return project + "/" + name }

func emptyState() State {
	return State{
		Projects: map[string]*v1.Project{},
		Services: map[string]*v1.Service{},
		Tasks:    map[string]*v1.Task{},
		Git:      map[string]*v1.GitStatus{},
	}
}

// StateFromSnapshot builds a State.
func StateFromSnapshot(rev uint64, snap *v1.Snapshot) State {
	s := emptyState()
	s.Revision = rev
	s.Daemon = snap.GetDaemon()
	for _, p := range snap.GetProjects() {
		s.Projects[p.GetId()] = p
	}
	for _, sv := range snap.GetServices() {
		s.Services[Key(sv.GetProject(), sv.GetName())] = sv
	}
	for _, tk := range snap.GetTasks() {
		s.Tasks[Key(tk.GetProject(), tk.GetName())] = tk
	}
	for _, g := range snap.GetGit() {
		s.Git[g.GetProject()] = g
	}
	return s
}

func (s State) clone() State {
	c := emptyState()
	c.Revision = s.Revision
	c.Daemon = s.Daemon
	for k, v := range s.Projects {
		c.Projects[k] = v
	}
	for k, v := range s.Services {
		c.Services[k] = v
	}
	for k, v := range s.Tasks {
		c.Tasks[k] = v
	}
	for k, v := range s.Git {
		c.Git[k] = v
	}
	return c
}

// apply applies one change in place.
func (s *State) apply(rev uint64, ch *v1.Change) {
	s.Revision = rev
	switch x := ch.GetChange().(type) {
	case *v1.Change_Project:
		s.Projects[x.Project.GetId()] = x.Project
	case *v1.Change_Service:
		s.Services[Key(x.Service.GetProject(), x.Service.GetName())] = x.Service
	case *v1.Change_Task:
		s.Tasks[Key(x.Task.GetProject(), x.Task.GetName())] = x.Task
	case *v1.Change_Git:
		s.Git[x.Git.GetProject()] = x.Git
	case *v1.Change_Daemon:
		s.Daemon = x.Daemon
	case *v1.Change_Removed:
		r := x.Removed
		switch r.GetKind() {
		case "project":
			delete(s.Projects, r.GetProject())
			delete(s.Git, r.GetProject())
			for k, sv := range s.Services {
				if sv.GetProject() == r.GetProject() {
					delete(s.Services, k)
				}
			}
			for k, tk := range s.Tasks {
				if tk.GetProject() == r.GetProject() {
					delete(s.Tasks, k)
				}
			}
		case "service":
			delete(s.Services, Key(r.GetProject(), r.GetName()))
		case "task":
			delete(s.Tasks, Key(r.GetProject(), r.GetName()))
		case "git":
			delete(s.Git, r.GetProject())
		}
	}
}

// Project returns the project or nil.
func (s State) Project(id string) *v1.Project { return s.Projects[id] }

// Service returns the service or nil.
func (s State) Service(project, name string) *v1.Service { return s.Services[Key(project, name)] }

// Task returns the task or nil.
func (s State) Task(project, name string) *v1.Task { return s.Tasks[Key(project, name)] }

// ServicesOf returns a project's services sorted by name.
func (s State) ServicesOf(project string) []*v1.Service {
	var out []*v1.Service
	for _, sv := range s.Services {
		if sv.GetProject() == project {
			out = append(out, sv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
	return out
}

// ServiceStatus returns the service's status, or "" when absent.
func (s State) ServiceStatus(project, name string) string {
	return s.Service(project, name).GetStatus()
}

// Running reports whether the service is running with a pid.
func (s State) Running(project, name string) bool {
	sv := s.Service(project, name)
	return sv.GetStatus() == "running" && sv.GetPid() > 0
}

// AllRunning reports whether every named service (every service of the
// project when none are named) is running.
func (s State) AllRunning(project string, names ...string) bool {
	if len(names) == 0 {
		svcs := s.ServicesOf(project)
		if len(svcs) == 0 {
			return false
		}
		for _, sv := range svcs {
			names = append(names, sv.GetName())
		}
	}
	for _, n := range names {
		if !s.Running(project, n) {
			return false
		}
	}
	return true
}

// ServiceIs reports whether the service's status is one of statuses.
func (s State) ServiceIs(project, name string, statuses ...string) bool {
	st := s.ServiceStatus(project, name)
	for _, want := range statuses {
		if st == want {
			return true
		}
	}
	return false
}

// AllServicesIn reports whether every service of project has one of statuses.
func (s State) AllServicesIn(project string, statuses ...string) bool {
	svcs := s.ServicesOf(project)
	if len(svcs) == 0 {
		return false
	}
	for _, sv := range svcs {
		if !s.ServiceIs(project, sv.GetName(), statuses...) {
			return false
		}
	}
	return true
}

// ServicePid returns the service's pid (0 when absent/not running).
func (s State) ServicePid(project, name string) int {
	return int(s.Service(project, name).GetPid())
}

// TaskRunning reports whether the task is running with a pid.
func (s State) TaskRunning(project, name string) bool {
	tk := s.Task(project, name)
	return tk.GetStatus() == "running" && tk.GetPid() > 0
}

// TaskStatus returns the task's status, or "".
func (s State) TaskStatus(project, name string) string { return s.Task(project, name).GetStatus() }

// String pretty-prints the state for failure messages.
func (s State) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "state @rev %d\n", s.Revision)
	if d := s.Daemon; d != nil {
		fmt.Fprintf(&b, "  daemon pid=%d web=%s proxy=%s draining=%v version=%s\n", d.GetPid(), d.GetWebAddr(), d.GetProxyAddr(), d.GetDraining(), d.GetVersion())
	}
	ids := make([]string, 0, len(s.Projects))
	for id := range s.Projects {
		ids = append(ids, id)
	}
	for _, sv := range s.Services {
		if _, ok := s.Projects[sv.GetProject()]; !ok {
			ids = append(ids, sv.GetProject())
		}
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if p := s.Projects[id]; p != nil {
			fmt.Fprintf(&b, "  project %s status=%s desired=%s running=%d/%d error=%q config=%s\n", id, p.GetStatus(), p.GetDesired(), p.GetServicesRunning(), p.GetServicesTotal(), p.GetError(), p.GetConfigPath())
		} else {
			fmt.Fprintf(&b, "  project %s (not in project list)\n", id)
		}
		for _, sv := range s.ServicesOf(id) {
			fmt.Fprintf(&b, "    service %-12s %s\n", sv.GetName(), FormatService(sv))
		}
		var tasks []*v1.Task
		for _, tk := range s.Tasks {
			if tk.GetProject() == id {
				tasks = append(tasks, tk)
			}
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].GetName() < tasks[j].GetName() })
		for _, tk := range tasks {
			fmt.Fprintf(&b, "    task    %-12s status=%s pid=%d exit=%d run=%d msg=%q args=%v\n", tk.GetName(), tk.GetStatus(), tk.GetPid(), tk.GetExitCode(), tk.GetRun(), tk.GetMessage(), tk.GetArgs())
		}
		if g := s.Git[id]; g != nil {
			fmt.Fprintf(&b, "    git repo=%v branch=%s dirty=%d staged=%d untracked=%d seq=%d\n", g.GetIsRepo(), g.GetBranch(), g.GetDirty(), g.GetStaged(), g.GetUntracked(), g.GetChangeSeq())
		}
	}
	return b.String()
}

// FormatService renders the dynamic fields of a service on one line.
func FormatService(sv *v1.Service) string {
	return fmt.Sprintf("status=%s health=%s pid=%d exit=%d restarts=%d run=%d msg=%q urls=%v",
		sv.GetStatus(), sv.GetHealth(), sv.GetPid(), sv.GetExitCode(), sv.GetRestarts(), sv.GetRun(), sv.GetMessage(), sv.GetUrls())
}

// DiffServices compares status and pid of every service in a and b and
// returns a description of the differences ("" when equal).
func DiffServices(a, b State) string {
	var diffs []string
	keys := map[string]bool{}
	for k := range a.Services {
		keys[k] = true
	}
	for k := range b.Services {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		x, y := a.Services[k], b.Services[k]
		switch {
		case x == nil:
			diffs = append(diffs, fmt.Sprintf("%s: missing on left", k))
		case y == nil:
			diffs = append(diffs, fmt.Sprintf("%s: missing on right", k))
		case x.GetStatus() != y.GetStatus() || x.GetPid() != y.GetPid() || x.GetRun() != y.GetRun():
			diffs = append(diffs, fmt.Sprintf("%s: %s/%d/run%d vs %s/%d/run%d", k, x.GetStatus(), x.GetPid(), x.GetRun(), y.GetStatus(), y.GetPid(), y.GetRun()))
		}
	}
	return strings.Join(diffs, "\n")
}
