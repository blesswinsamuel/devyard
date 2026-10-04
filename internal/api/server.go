package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/events"
	pb "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
	"github.com/blesswinsamuel/devyard/internal/gitlog"
	"github.com/blesswinsamuel/devyard/internal/gitstate"
	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/ports"
	"github.com/blesswinsamuel/devyard/internal/sessions"
)

// Daemon is the daemon-level control surface used by the API.
type Daemon interface {
	Info() *pb.DaemonInfo
	// RequestStop asks the daemon to stop all services and exit.
	RequestStop()
	// RequestRestart asks the daemon to hand over to a fresh daemon
	// process, optionally restarting services.
	RequestRestart(restartServices bool)
	GlobalConfig() (*globalconfig.Config, string, error)
	SaveGlobalConfig(*globalconfig.Config) error
	// SetWebPassword sets the dashboard password (bcrypt-hashed) and
	// applies it without a restart. ClearWebPassword removes it.
	SetWebPassword(password string) error
	ClearWebPassword() error
}

// Server implements devyardv1connect.DaemonServiceHandler.
type Server struct {
	Mgr      *engine.Manager
	Bus      *events.Bus
	Git      *gitstate.Tracker
	Sessions *sessions.Manager
	Daemon   Daemon
	Log      *slog.Logger
}

var _ devyardv1connect.DaemonServiceHandler = (*Server)(nil)

const heartbeatInterval = 15 * time.Second

// --- daemon ------------------------------------------------------------------

func (s *Server) GetDaemon(ctx context.Context, _ *connect.Request[pb.GetDaemonRequest]) (*connect.Response[pb.GetDaemonResponse], error) {
	return connect.NewResponse(&pb.GetDaemonResponse{Info: s.Daemon.Info()}), nil
}

func (s *Server) StopDaemon(ctx context.Context, _ *connect.Request[pb.StopDaemonRequest]) (*connect.Response[pb.StopDaemonResponse], error) {
	s.Daemon.RequestStop()
	return connect.NewResponse(&pb.StopDaemonResponse{}), nil
}

func (s *Server) RestartDaemon(ctx context.Context, req *connect.Request[pb.RestartDaemonRequest]) (*connect.Response[pb.RestartDaemonResponse], error) {
	s.Daemon.RequestRestart(req.Msg.RestartServices)
	return connect.NewResponse(&pb.RestartDaemonResponse{}), nil
}

func (s *Server) GetGlobalConfig(ctx context.Context, _ *connect.Request[pb.GetGlobalConfigRequest]) (*connect.Response[pb.GetGlobalConfigResponse], error) {
	cfg, path, err := s.Daemon.GlobalConfig()
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.GetGlobalConfigResponse{Config: globalConfigToProto(cfg), Path: path}), nil
}

func (s *Server) UpdateGlobalConfig(ctx context.Context, req *connect.Request[pb.UpdateGlobalConfigRequest]) (*connect.Response[pb.UpdateGlobalConfigResponse], error) {
	if req.Msg.Config == nil {
		return nil, invalid("config is required")
	}
	cfg := globalConfigFromProto(req.Msg.Config)
	if err := cfg.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.Daemon.SaveGlobalConfig(cfg); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.UpdateGlobalConfigResponse{}), nil
}

func (s *Server) SetWebPassword(ctx context.Context, req *connect.Request[pb.SetWebPasswordRequest]) (*connect.Response[pb.SetWebPasswordResponse], error) {
	if req.Msg.Clear {
		if err := s.Daemon.ClearWebPassword(); err != nil {
			return nil, toConnect(err)
		}
	} else {
		if req.Msg.Password == "" {
			return nil, invalid("password is required (or clear)")
		}
		if err := s.Daemon.SetWebPassword(req.Msg.Password); err != nil {
			return nil, toConnect(err)
		}
	}
	return connect.NewResponse(&pb.SetWebPasswordResponse{}), nil
}

func globalConfigToProto(c *globalconfig.Config) *pb.GlobalConfig {
	return &pb.GlobalConfig{
		Web: &pb.GlobalWebConfig{
			Host:         c.Web.Host,
			Port:         int32(c.Web.Port),
			AllowedHosts: c.Web.AllowedHosts,
			PasswordSet:  c.Web.PasswordHash != "",
		},
		Proxy: &pb.GlobalProxyConfig{
			Host:         c.Proxy.Host,
			Port:         int32(c.Proxy.Port),
			DomainSuffix: c.Proxy.DomainSuffix,
			Tls: &pb.GlobalProxyTLSConfig{
				Enabled:      c.Proxy.TLS.Enabled,
				Port:         int32(c.Proxy.TLS.Port),
				CertFile:     c.Proxy.TLS.CertFile,
				KeyFile:      c.Proxy.TLS.KeyFile,
				HttpRedirect: c.Proxy.TLS.HTTPRedirect,
			},
		},
	}
}

func globalConfigFromProto(p *pb.GlobalConfig) *globalconfig.Config {
	c := globalconfig.Defaults()
	if w := p.GetWeb(); w != nil {
		if w.Host != "" {
			c.Web.Host = w.Host
		}
		c.Web.Port = int(w.Port)
		c.Web.AllowedHosts = w.AllowedHosts
	}
	if px := p.GetProxy(); px != nil {
		if px.Host != "" {
			c.Proxy.Host = px.Host
		}
		c.Proxy.Port = int(px.Port)
		if px.DomainSuffix != "" {
			c.Proxy.DomainSuffix = px.DomainSuffix
		}
		if t := px.GetTls(); t != nil {
			c.Proxy.TLS = globalconfig.ProxyTLSConfig{
				Enabled:      t.Enabled,
				Port:         int(t.Port),
				CertFile:     t.CertFile,
				KeyFile:      t.KeyFile,
				HTTPRedirect: t.HttpRedirect,
			}
		}
	}
	return &c
}

// --- state -------------------------------------------------------------------

func (s *Server) Watch(ctx context.Context, _ *connect.Request[pb.WatchRequest], stream *connect.ServerStream[pb.WatchResponse]) error {
	sub, snap := s.Bus.Subscribe()
	defer sub.Close()
	if err := stream.Send(snap); err != nil {
		return err
	}
	for {
		next, cancel := context.WithTimeout(ctx, heartbeatInterval)
		ev, err := sub.Next(next)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			ev = &pb.WatchResponse{Event: &pb.WatchResponse_Heartbeat{Heartbeat: &pb.Heartbeat{}}}
		}
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
}

func (s *Server) GetState(ctx context.Context, _ *connect.Request[pb.GetStateRequest]) (*connect.Response[pb.GetStateResponse], error) {
	rev, snap := s.Bus.Snapshot()
	return connect.NewResponse(&pb.GetStateResponse{Revision: rev, Snapshot: snap}), nil
}

// --- projects ----------------------------------------------------------------

func (s *Server) AddProject(ctx context.Context, req *connect.Request[pb.AddProjectRequest]) (*connect.Response[pb.AddProjectResponse], error) {
	if req.Msg.ConfigPath == "" {
		return nil, invalid("config_path is required")
	}
	var env []string
	if len(req.Msg.Env) > 0 {
		env = req.Msg.Env
	}
	p, err := s.Mgr.Add(ctx, engine.AddOptions{
		ConfigPath: req.Msg.ConfigPath,
		Env:        env,
		Start:      req.Msg.Start,
		Build:      req.Msg.Build,
	})
	resp := &pb.AddProjectResponse{}
	if p != nil {
		resp.Project = s.Bus.Project(p.ID())
	}
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) project(id string) (*engine.Project, error) {
	if id == "" {
		return nil, invalid("project is required")
	}
	p, err := s.Mgr.Get(id)
	return p, toConnect(err)
}

func (s *Server) StartProject(ctx context.Context, req *connect.Request[pb.StartProjectRequest]) (*connect.Response[pb.StartProjectResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := p.Start(ctx, req.Msg.Services, req.Msg.Build); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.StartProjectResponse{}), nil
}

func (s *Server) StopProject(ctx context.Context, req *connect.Request[pb.StopProjectRequest]) (*connect.Response[pb.StopProjectResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := p.Stop(ctx); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.StopProjectResponse{}), nil
}

func (s *Server) RestartProject(ctx context.Context, req *connect.Request[pb.RestartProjectRequest]) (*connect.Response[pb.RestartProjectResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := p.Restart(ctx, req.Msg.Build); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.RestartProjectResponse{}), nil
}

func (s *Server) ReloadProject(ctx context.Context, req *connect.Request[pb.ReloadProjectRequest]) (*connect.Response[pb.ReloadProjectResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := p.Reload(ctx, req.Msg.Env, req.Msg.UpdateEnv); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.ReloadProjectResponse{}), nil
}

func (s *Server) RemoveProject(ctx context.Context, req *connect.Request[pb.RemoveProjectRequest]) (*connect.Response[pb.RemoveProjectResponse], error) {
	if req.Msg.Project == "" {
		return nil, invalid("project is required")
	}
	if err := s.Mgr.Remove(ctx, req.Msg.Project); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.RemoveProjectResponse{}), nil
}

// --- services ----------------------------------------------------------------

func (s *Server) service(project, name string) (*engine.Service, error) {
	p, err := s.project(project)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, invalid("service is required")
	}
	svc, err := p.Service(name)
	return svc, toConnect(err)
}

func (s *Server) StartService(ctx context.Context, req *connect.Request[pb.StartServiceRequest]) (*connect.Response[pb.StartServiceResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if req.Msg.Service == "" {
		return nil, invalid("service is required")
	}
	if err := p.StartService(ctx, req.Msg.Service, req.Msg.Build); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.StartServiceResponse{}), nil
}

func (s *Server) StopService(ctx context.Context, req *connect.Request[pb.StopServiceRequest]) (*connect.Response[pb.StopServiceResponse], error) {
	svc, err := s.service(req.Msg.Project, req.Msg.Service)
	if err != nil {
		return nil, err
	}
	if err := svc.Stop(ctx); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.StopServiceResponse{}), nil
}

func (s *Server) RestartService(ctx context.Context, req *connect.Request[pb.RestartServiceRequest]) (*connect.Response[pb.RestartServiceResponse], error) {
	svc, err := s.service(req.Msg.Project, req.Msg.Service)
	if err != nil {
		return nil, err
	}
	if err := svc.Restart(ctx, req.Msg.Build); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.RestartServiceResponse{}), nil
}

func (s *Server) KillService(ctx context.Context, req *connect.Request[pb.KillServiceRequest]) (*connect.Response[pb.KillServiceResponse], error) {
	svc, err := s.service(req.Msg.Project, req.Msg.Service)
	if err != nil {
		return nil, err
	}
	if err := svc.Kill(ctx, req.Msg.Signal); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.KillServiceResponse{}), nil
}

// --- tasks -------------------------------------------------------------------

func (s *Server) task(project, name string) (*engine.Task, error) {
	p, err := s.project(project)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, invalid("task is required")
	}
	t, err := p.Task(name)
	return t, toConnect(err)
}

func (s *Server) RunTask(ctx context.Context, req *connect.Request[pb.RunTaskRequest]) (*connect.Response[pb.RunTaskResponse], error) {
	p, err := s.project(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if req.Msg.Task == "" {
		return nil, invalid("task is required")
	}
	// The run belongs to the daemon, not to this request: a client that
	// disconnects must not cancel it.
	run, err := p.RunTask(context.WithoutCancel(ctx), req.Msg.Task, req.Msg.Args)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.RunTaskResponse{Run: run}), nil
}

func (s *Server) StopTask(ctx context.Context, req *connect.Request[pb.StopTaskRequest]) (*connect.Response[pb.StopTaskResponse], error) {
	t, err := s.task(req.Msg.Project, req.Msg.Task)
	if err != nil {
		return nil, err
	}
	if err := t.Stop(ctx); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.StopTaskResponse{}), nil
}

func (s *Server) KillTask(ctx context.Context, req *connect.Request[pb.KillTaskRequest]) (*connect.Response[pb.KillTaskResponse], error) {
	t, err := s.task(req.Msg.Project, req.Msg.Task)
	if err != nil {
		return nil, err
	}
	if err := t.Kill(ctx, req.Msg.Signal); err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&pb.KillTaskResponse{}), nil
}

// --- ports -------------------------------------------------------------------

func (s *Server) ListPorts(ctx context.Context, req *connect.Request[pb.ListPortsRequest]) (*connect.Response[pb.ListPortsResponse], error) {
	type owner struct{ project, name string }
	owners := map[int]owner{}
	var pgids []int
	for _, svc := range s.Bus.Services(req.Msg.Project) {
		if svc.Pid > 0 {
			owners[int(svc.Pid)] = owner{svc.Project, svc.Name}
			pgids = append(pgids, int(svc.Pid))
		}
	}
	for _, t := range s.Bus.Tasks(req.Msg.Project) {
		if t.Pid > 0 {
			owners[int(t.Pid)] = owner{t.Project, t.Name}
			pgids = append(pgids, int(t.Pid))
		}
	}
	resp := &pb.ListPortsResponse{}
	if len(pgids) == 0 {
		return connect.NewResponse(resp), nil
	}
	bindings, err := ports.InspectPGIDs(pgids)
	if err != nil {
		return nil, toConnect(err)
	}
	for _, b := range bindings {
		o := owners[b.PGID]
		resp.Ports = append(resp.Ports, &pb.PortBinding{
			Project:  o.project,
			Service:  o.name,
			Pid:      int32(b.PID),
			Ip:       b.IP,
			Port:     int32(b.Port),
			Protocol: b.Protocol,
		})
	}
	return connect.NewResponse(resp), nil
}

// --- git ---------------------------------------------------------------------

func (s *Server) gitDir(project string) (string, error) {
	if _, err := s.project(project); err != nil {
		return "", err
	}
	dir, ok := s.Git.Dir(project)
	if !ok {
		return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s has no repository", project))
	}
	return dir, nil
}

func (s *Server) GitLog(ctx context.Context, req *connect.Request[pb.GitLogRequest]) (*connect.Response[pb.GitLogResponse], error) {
	dir, err := s.gitDir(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	commits, branches, tags, stashes, err := gitlog.Log(dir)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&pb.GitLogResponse{Commits: commits, Branches: branches, Tags: tags, Stashes: stashes}), nil
}

func (s *Server) GitDiff(ctx context.Context, req *connect.Request[pb.GitDiffRequest]) (*connect.Response[pb.GitDiffResponse], error) {
	dir, err := s.gitDir(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if req.Msg.Hash == "" {
		return nil, invalid("hash is required")
	}
	var res *pb.GitDiffResult
	if req.Msg.ContextLines > 0 {
		res, err = gitlog.Diff(dir, req.Msg.Hash, req.Msg.Path, int(req.Msg.ContextLines))
	} else {
		res, err = gitlog.Diff(dir, req.Msg.Hash, req.Msg.Path)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&pb.GitDiffResponse{Result: res}), nil
}

func (s *Server) GitStage(ctx context.Context, req *connect.Request[pb.GitStageRequest]) (*connect.Response[pb.GitStageResponse], error) {
	dir, err := s.gitDir(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := gitlog.Stage(dir, req.Msg.Path, req.Msg.StageAll, req.Msg.Unstage); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	s.Git.Changed(req.Msg.Project)
	return connect.NewResponse(&pb.GitStageResponse{}), nil
}

func (s *Server) GitCommit(ctx context.Context, req *connect.Request[pb.GitCommitRequest]) (*connect.Response[pb.GitCommitResponse], error) {
	dir, err := s.gitDir(req.Msg.Project)
	if err != nil {
		return nil, err
	}
	if err := gitlog.Commit(dir, req.Msg.Message); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	s.Git.Changed(req.Msg.Project)
	return connect.NewResponse(&pb.GitCommitResponse{}), nil
}

func (s *Server) gitRemote(ctx context.Context, project, op string) (string, error) {
	if _, err := s.gitDir(project); err != nil {
		return "", err
	}
	out, err := s.Git.Remote(ctx, project, op)
	if err != nil {
		if errors.Is(err, gitstate.ErrBusy) {
			return "", toConnect(err)
		}
		return "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return out, nil
}

func (s *Server) GitPush(ctx context.Context, req *connect.Request[pb.GitPushRequest]) (*connect.Response[pb.GitPushResponse], error) {
	out, err := s.gitRemote(ctx, req.Msg.Project, "push")
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GitPushResponse{Output: out}), nil
}

func (s *Server) GitPull(ctx context.Context, req *connect.Request[pb.GitPullRequest]) (*connect.Response[pb.GitPullResponse], error) {
	out, err := s.gitRemote(ctx, req.Msg.Project, "pull")
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GitPullResponse{Output: out}), nil
}

func (s *Server) GitFetch(ctx context.Context, req *connect.Request[pb.GitFetchRequest]) (*connect.Response[pb.GitFetchResponse], error) {
	out, err := s.gitRemote(ctx, req.Msg.Project, "fetch")
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GitFetchResponse{Output: out}), nil
}
