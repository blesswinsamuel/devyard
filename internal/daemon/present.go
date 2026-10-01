//go:build unix

package daemon

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/events"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gitstate"
)

// urlConfig describes how proxied service URLs are formed.
type urlConfig struct {
	Suffix string
	Port   int
	TLS    bool
}

// presenter turns engine state into API entities on the bus.
type presenter struct {
	bus *events.Bus
	git *gitstate.Tracker

	mu       sync.Mutex
	urls     urlConfig
	defaults map[string]string // project -> default service
}

var _ engine.Observer = (*presenter)(nil)

func newPresenter(bus *events.Bus, git *gitstate.Tracker, urls urlConfig) *presenter {
	return &presenter{bus: bus, git: git, urls: urls, defaults: map[string]string{}}
}

func (p *presenter) ProjectChanged(st engine.ProjectState) {
	p.mu.Lock()
	p.defaults[st.ID] = st.Primary
	p.mu.Unlock()
	p.bus.UpsertProject(&pb.Project{
		Id:              st.ID,
		ConfigPath:      st.ConfigPath,
		EnvFiles:        st.EnvFiles,
		Status:          st.Status,
		Desired:         st.Desired,
		Error:           st.Error,
		ServicesTotal:   int32(st.ServicesTotal),
		ServicesRunning: int32(st.ServicesActive),
		Primary:         st.Primary,
		Links:           links(st.Links),
		UpdatedAtUnixMs: st.UpdatedAt.UnixMilli(),
	})
	if p.git != nil && st.ConfigPath != "" {
		p.git.Track(st.ID, st.ConfigPath)
	}
}

func (p *presenter) ProjectRemoved(id string) {
	p.mu.Lock()
	delete(p.defaults, id)
	p.mu.Unlock()
	if p.git != nil {
		p.git.Untrack(id)
	}
	p.bus.RemoveProject(id)
}

func ms(t interface{ UnixMilli() int64 }) int64 { return t.UnixMilli() }

func (p *presenter) ServiceChanged(def *engine.ProcessDef, st engine.ServiceState) {
	p.mu.Lock()
	isDefault := p.defaults[def.Project] == def.Name
	urls := p.urls
	p.mu.Unlock()
	svc := &pb.Service{
		Project:      def.Project,
		Name:         def.Name,
		Status:       st.Status,
		Health:       st.Health,
		HealthDetail: st.HealthDetail,
		Pid:          int32(st.PID),
		ExitCode:     int32(st.ExitCode),
		Restarts:     int32(st.Restarts),
		Run:          st.Run,
		Message:      st.Message,
		Urls:         serviceURLs(def, isDefault, urls),
		Order:        int32(def.Order),
		Spec:         serviceSpec(def),
	}
	if !st.StartedAt.IsZero() {
		svc.StartedAtUnixMs = ms(st.StartedAt)
	}
	if !st.FinishedAt.IsZero() {
		svc.FinishedAtUnixMs = ms(st.FinishedAt)
	}
	if !st.NextRestartAt.IsZero() {
		svc.NextRestartAtUnixMs = ms(st.NextRestartAt)
	}
	p.bus.UpsertService(svc)
}

func (p *presenter) ServiceRemoved(project, name string) { p.bus.RemoveService(project, name) }

func (p *presenter) TaskChanged(def *engine.ProcessDef, st engine.TaskState) {
	t := &pb.Task{
		Project:  def.Project,
		Name:     def.Name,
		Status:   st.Status,
		Pid:      int32(st.PID),
		ExitCode: int32(st.ExitCode),
		Run:      st.Run,
		Message:  st.Message,
		Args:     st.Args,
		Spec: &pb.TaskSpec{
			Command:   def.Cmd.String(),
			Dir:       def.Dir,
			Tty:       def.TTY,
			DependsOn: def.Deps,
			EnvKeys:   def.EnvKeys,
		},
	}
	if !st.StartedAt.IsZero() {
		t.StartedAtUnixMs = ms(st.StartedAt)
	}
	if !st.FinishedAt.IsZero() {
		t.FinishedAtUnixMs = ms(st.FinishedAt)
	}
	p.bus.UpsertTask(t)
}

func (p *presenter) TaskRemoved(project, name string) { p.bus.RemoveTask(project, name) }

func links(ls []config.Link) []*pb.Link {
	out := make([]*pb.Link, len(ls))
	for i, l := range ls {
		out[i] = &pb.Link{Name: l.Name, Url: l.URL}
	}
	return out
}

func serviceSpec(def *engine.ProcessDef) *pb.ServiceSpec {
	spec := &pb.ServiceSpec{
		Command:    def.Cmd.String(),
		Dir:        def.Dir,
		Restart:    string(def.Restart),
		DependsOn:  def.Deps,
		Tty:        def.TTY,
		EnvKeys:    def.EnvKeys,
		Autostart:  def.Autostart,
		StopSignal: def.StopSignal,
	}
	if r := def.Ready; r != nil {
		spec.Ready = &pb.Ready{
			Kind:          r.Kind(),
			Target:        r.Target(),
			IntervalMs:    r.Interval.Milliseconds(),
			TimeoutMs:     r.Timeout.Milliseconds(),
			Retries:       int32(r.Retries),
			StartPeriodMs: r.StartPeriod.Milliseconds(),
		}
	}
	for _, port := range def.Ports {
		spec.Ports = append(spec.Ports, &pb.Port{Name: port.Name, Port: int32(port.Port), Auto: port.Auto})
	}
	if def.Build != nil {
		spec.BuildCommand = def.Build.Display
	}
	return spec
}

// serviceURLs lists the proxy URLs of a service in display order: the
// project URL (default service), the service URL, then named ports.
func serviceURLs(def *engine.ProcessDef, isDefault bool, u urlConfig) []string {
	if len(def.Ports) == 0 || u.Suffix == "" {
		return nil
	}
	scheme := "http"
	defPort := 80
	if u.TLS {
		scheme = "https"
		defPort = 443
	}
	hostport := func(host string) string {
		if u.Port == defPort || u.Port == 0 {
			return host
		}
		return net.JoinHostPort(host, strconv.Itoa(u.Port))
	}
	label := def.Name
	if def.ProxyHost != "" {
		label = def.ProxyHost
	}
	base := def.Project + "." + u.Suffix
	var out []string
	if isDefault {
		out = append(out, fmt.Sprintf("%s://%s", scheme, hostport(base)))
	}
	out = append(out, fmt.Sprintf("%s://%s", scheme, hostport(label+"."+base)))
	for _, port := range def.Ports {
		if port.Name != "" {
			out = append(out, fmt.Sprintf("%s://%s", scheme, hostport(port.Name+"."+label+"."+base)))
		}
	}
	return out
}
