package supervisor

import (
	"context"

	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/protocol"
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
func (b *ControlBackend) States() []protocol.ServiceState {
	in := b.s.States()
	out := make([]protocol.ServiceState, len(in))
	for i, st := range in {
		out[i] = protocol.ServiceState{
			Name:       st.Name,
			Status:     string(st.Status),
			PID:        st.PID,
			ExitCode:   st.ExitCode,
			Restarts:   st.Restarts,
			StartedAt:  protocol.FormatTime(st.StartedAt),
			FinishedAt: protocol.FormatTime(st.FinishedAt),
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

// StopService stops a single service in place (used by the TUI's stop-selected
// keybinding). markStopped=true so unless-stopped does not auto-resume it.
func (b *ControlBackend) StopService(name string) error {
	return b.s.StopService(name, true)
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
func (b *ControlBackend) Top(name string) ([]protocol.ServiceStat, error) {
	in, err := b.s.Top(name)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.ServiceStat, len(in))
	for i, st := range in {
		out[i] = protocol.ServiceStat{
			Name:     st.Name,
			Status:   string(st.Status),
			PID:      st.PID,
			PGID:     st.PGID,
			Procs:    st.Procs,
			CPU:      st.CPU,
			RSSBytes: st.RSS,
		}
	}
	return out, nil
}

// LogPath returns the absolute path of a service's log file.
func (b *ControlBackend) LogPath(name string) (string, error) {
	return b.s.LogPath(name)
}

// PreviousLogPath returns the absolute path of a service's previous-run log file.
func (b *ControlBackend) PreviousLogPath(name string) (string, error) {
	return b.s.PreviousLogPath(name)
}

// Compile-time assertion that ControlBackend satisfies control.Backend.
var _ control.Backend = (*ControlBackend)(nil)
