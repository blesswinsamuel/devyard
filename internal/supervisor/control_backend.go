package supervisor

import (
	"context"
	"io"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/protocol"
)

// ControlBackend adapts a *Supervisor to the control.Backend interface so a
// control.Server can drive it. The adapter is the only place that knows how to
// translate the supervisor's rich ServiceState into the wire-shaped
// protocol.ServiceState; it lives here (next to the type it converts from)
// rather than in internal/control so that package stays free of a supervisor
// dependency.
type ControlBackend struct {
	s *Supervisor
}

// NewControlBackend wraps s for use with a control.Server.
func NewControlBackend(s *Supervisor) *ControlBackend {
	return &ControlBackend{s: s}
}

// States returns a wire snapshot of every service in start order.
func (b *ControlBackend) States() []*protocol.ServiceState {
	in := b.s.States()
	out := make([]*protocol.ServiceState, len(in))
	for i, st := range in {
		out[i] = &protocol.ServiceState{
			Name:       st.Name,
			Status:     string(st.Status),
			Pid:        int32(st.PID),
			ExitCode:   int32(st.ExitCode),
			Restarts:   int32(st.Restarts),
			StartedAt:  protocol.TimeToProto(st.StartedAt),
			FinishedAt: protocol.TimeToProto(st.FinishedAt),
			HasHealth:  st.HasHealth,
			Health:     st.Health,
		}
	}
	return out
}

// Stop gracefully stops every service (used by `down`).
func (b *ControlBackend) Stop(ctx context.Context) error {
	return b.s.Stop(ctx)
}

// StopService stops a single service in place (used by `stop <service>`).
func (b *ControlBackend) StopService(name string) error {
	return b.s.StopService(name)
}

// StartService starts a single stopped service in place, leaving other stopped
// services alone (used by `start <service>` on a running project).
func (b *ControlBackend) StartService(name string) error {
	return b.s.StartService(name)
}

// KillService immediately sends signal to a single service's process group
// without a grace period. Empty signal means SIGKILL.
func (b *ControlBackend) KillService(name, signal string) error {
	return b.s.KillService(name, signal)
}

// Restart stops and relaunches one service by name.
func (b *ControlBackend) Restart(name string) error {
	return b.s.Restart(name)
}

// Top returns a wire snapshot of per-service CPU/memory usage for one service
// (or all when name is empty). It blocks for the supervisor's sampling
// interval while the daemon derives CPU usage.
func (b *ControlBackend) Top(name string) ([]*protocol.ServiceStat, error) {
	in, err := b.s.Top(name)
	if err != nil {
		return nil, err
	}
	out := make([]*protocol.ServiceStat, len(in))
	for i, st := range in {
		out[i] = &protocol.ServiceStat{
			Name:     st.Name,
			Status:   string(st.Status),
			Pid:      int32(st.PID),
			Pgid:     int32(st.PGID),
			Procs:    int32(st.Procs),
			Cpu:      st.CPU,
			RssBytes: st.RSS,
		}
	}
	return out, nil
}

// ListPorts returns a list of open listening sockets for all running services in the project.
func (b *ControlBackend) ListPorts() ([]*protocol.PortBinding, error) {
	return b.s.ListPorts()
}

// LogPath returns the absolute path of a service's log file.
func (b *ControlBackend) LogPath(name string) (string, error) {
	return b.s.LogPath(name)
}

// PreviousLogPath returns the absolute path of a service's previous-run log file.
func (b *ControlBackend) PreviousLogPath(name string) (string, error) {
	return b.s.PreviousLogPath(name)
}

// ListTasks returns a snapshot of defined tasks and runtime states for the project.
func (b *ControlBackend) ListTasks() []*protocol.TaskState {
	return b.s.ListTasks()
}

// RunTask executes a named task command in a dedicated process group, streaming output to out.
func (b *ControlBackend) RunTask(ctx context.Context, name string, args []string, out io.Writer) (int, error) {
	return b.s.RunTask(ctx, name, args, out)
}

// StopTask stops a running task by name.
func (b *ControlBackend) StopTask(name string) error {
	return b.s.StopTask(name)
}

// TaskLogPath returns the absolute path of a task's log file.
func (b *ControlBackend) TaskLogPath(name string) (string, error) {
	return b.s.TaskLogPath(name)
}

// TaskPreviousLogPath returns the absolute path of a task's previous-run log file.
func (b *ControlBackend) TaskPreviousLogPath(name string) (string, error) {
	return b.s.TaskPreviousLogPath(name)
}

// Compile-time assertion that ControlBackend satisfies control.Backend.
var _ control.Backend = (*ControlBackend)(nil)
