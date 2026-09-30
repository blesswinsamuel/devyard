// Package events is the daemon's state bus: a materialized view of every
// entity (projects, services, tasks, git status, daemon info) plus a
// revisioned change stream.
//
// Subscribers receive a snapshot followed by changes. Delivery never blocks
// publishers: a subscriber that falls behind is flagged and receives a fresh
// snapshot instead of silently missing changes.
package events

import (
	"context"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
)

// Entity kinds.
const (
	KindProject = "project"
	KindService = "service"
	KindTask    = "task"
	KindGit     = "git"
)

const subscriberBuffer = 1024

// Bus holds the current state and fans out changes.
type Bus struct {
	mu       sync.Mutex
	rev      uint64
	daemon   *pb.DaemonInfo
	projects map[string]*pb.Project
	services map[string]*pb.Service
	tasks    map[string]*pb.Task
	git      map[string]*pb.GitStatus
	subs     map[*Subscription]struct{}
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{
		projects: map[string]*pb.Project{},
		services: map[string]*pb.Service{},
		tasks:    map[string]*pb.Task{},
		git:      map[string]*pb.GitStatus{},
		subs:     map[*Subscription]struct{}{},
	}
}

func key(project, name string) string { return project + "/" + name }

// Subscription is one Watch stream.
type Subscription struct {
	bus      *Bus
	ch       chan *pb.WatchResponse
	overflow bool // guarded by bus.mu
	resync   chan struct{}
}

// Subscribe registers a subscriber and returns it with its initial
// snapshot, atomically.
func (b *Bus) Subscribe() (*Subscription, *pb.WatchResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &Subscription{bus: b, ch: make(chan *pb.WatchResponse, subscriberBuffer), resync: make(chan struct{}, 1)}
	b.subs[s] = struct{}{}
	return s, b.snapshotLocked()
}

// Close unregisters the subscriber.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}

// Next returns the next event. After an overflow it returns a fresh
// snapshot, never a partial sequence.
func (s *Subscription) Next(ctx context.Context) (*pb.WatchResponse, error) {
	select {
	case <-s.resync:
		return s.fresh(), nil
	default:
	}
	select {
	case ev := <-s.ch:
		return ev, nil
	case <-s.resync:
		return s.fresh(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Subscription) fresh() *pb.WatchResponse {
	b := s.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(s.ch) > 0 {
		<-s.ch
	}
	s.overflow = false
	return b.snapshotLocked()
}

func (b *Bus) snapshotLocked() *pb.WatchResponse {
	snap := &pb.Snapshot{Daemon: b.daemon}
	for _, id := range sortedKeys(b.projects) {
		snap.Projects = append(snap.Projects, b.projects[id])
	}
	for _, k := range sortedKeys(b.services) {
		snap.Services = append(snap.Services, b.services[k])
	}
	for _, k := range sortedKeys(b.tasks) {
		snap.Tasks = append(snap.Tasks, b.tasks[k])
	}
	for _, k := range sortedKeys(b.git) {
		snap.Git = append(snap.Git, b.git[k])
	}
	return &pb.WatchResponse{Revision: b.rev, Event: &pb.WatchResponse_Snapshot{Snapshot: snap}}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// publishLocked assigns the next revision and fans the change out.
func (b *Bus) publishLocked(c *pb.Change) {
	b.rev++
	ev := &pb.WatchResponse{Revision: b.rev, Event: &pb.WatchResponse_Change{Change: c}}
	for s := range b.subs {
		if s.overflow {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			s.overflow = true
			select {
			case s.resync <- struct{}{}:
			default:
			}
		}
	}
}

// Snapshot returns the current state.
func (b *Bus) Snapshot() (uint64, *pb.Snapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ev := b.snapshotLocked()
	return ev.Revision, ev.GetSnapshot()
}

// SetDaemon publishes daemon info.
func (b *Bus) SetDaemon(d *pb.DaemonInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if proto.Equal(b.daemon, d) {
		return
	}
	b.daemon = d
	b.publishLocked(&pb.Change{Change: &pb.Change_Daemon{Daemon: d}})
}

// UpsertProject publishes a project.
func (b *Bus) UpsertProject(p *pb.Project) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.projects[p.Id]; ok && proto.Equal(old, p) {
		return
	}
	b.projects[p.Id] = p
	b.publishLocked(&pb.Change{Change: &pb.Change_Project{Project: p}})
}

// RemoveProject removes a project and everything that belongs to it.
func (b *Bus) RemoveProject(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, s := range b.services {
		if s.Project == id {
			delete(b.services, k)
			b.publishLocked(removed(KindService, id, s.Name))
		}
	}
	for k, t := range b.tasks {
		if t.Project == id {
			delete(b.tasks, k)
			b.publishLocked(removed(KindTask, id, t.Name))
		}
	}
	if _, ok := b.git[id]; ok {
		delete(b.git, id)
		b.publishLocked(removed(KindGit, id, ""))
	}
	if _, ok := b.projects[id]; ok {
		delete(b.projects, id)
		b.publishLocked(removed(KindProject, id, ""))
	}
}

func removed(kind, project, name string) *pb.Change {
	return &pb.Change{Change: &pb.Change_Removed{Removed: &pb.EntityRef{Kind: kind, Project: project, Name: name}}}
}

// UpsertService publishes a service.
func (b *Bus) UpsertService(s *pb.Service) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(s.Project, s.Name)
	if old, ok := b.services[k]; ok && proto.Equal(old, s) {
		return
	}
	b.services[k] = s
	b.publishLocked(&pb.Change{Change: &pb.Change_Service{Service: s}})
}

// RemoveService removes a service.
func (b *Bus) RemoveService(project, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(project, name)
	if _, ok := b.services[k]; !ok {
		return
	}
	delete(b.services, k)
	b.publishLocked(removed(KindService, project, name))
}

// UpsertTask publishes a task.
func (b *Bus) UpsertTask(t *pb.Task) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(t.Project, t.Name)
	if old, ok := b.tasks[k]; ok && proto.Equal(old, t) {
		return
	}
	b.tasks[k] = t
	b.publishLocked(&pb.Change{Change: &pb.Change_Task{Task: t}})
}

// RemoveTask removes a task.
func (b *Bus) RemoveTask(project, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := key(project, name)
	if _, ok := b.tasks[k]; !ok {
		return
	}
	delete(b.tasks, k)
	b.publishLocked(removed(KindTask, project, name))
}

// UpsertGit publishes a project's git status.
func (b *Bus) UpsertGit(g *pb.GitStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.git[g.Project]; ok && proto.Equal(old, g) {
		return
	}
	b.git[g.Project] = g
	b.publishLocked(&pb.Change{Change: &pb.Change_Git{Git: g}})
}

// Git returns a project's git status.
func (b *Bus) Git(project string) *pb.GitStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.git[project]
}

// Services returns the services of a project (all when project is "").
func (b *Bus) Services(project string) []*pb.Service {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*pb.Service
	for _, k := range sortedKeys(b.services) {
		if s := b.services[k]; project == "" || s.Project == project {
			out = append(out, s)
		}
	}
	return out
}

// Service returns one service.
func (b *Bus) Service(project, name string) *pb.Service {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.services[key(project, name)]
}

// Tasks returns the tasks of a project.
func (b *Bus) Tasks(project string) []*pb.Task {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*pb.Task
	for _, k := range sortedKeys(b.tasks) {
		if t := b.tasks[k]; project == "" || t.Project == project {
			out = append(out, t)
		}
	}
	return out
}

// Project returns one project.
func (b *Bus) Project(id string) *pb.Project {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.projects[id]
}

// Projects returns all projects.
func (b *Bus) Projects() []*pb.Project {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*pb.Project
	for _, id := range sortedKeys(b.projects) {
		out = append(out, b.projects[id])
	}
	return out
}
