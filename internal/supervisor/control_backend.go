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

// KillService immediately SIGKILLs a single service without a grace period.
func (b *ControlBackend) KillService(name string) error {
	return b.s.KillService(name)
}

// Restart stops and relaunches one service by name.
func (b *ControlBackend) Restart(name string) error {
	return b.s.Restart(name)
}

// LogPath returns the absolute path of a service's log file.
func (b *ControlBackend) LogPath(name string) (string, error) {
	return b.s.LogPath(name)
}

// Compile-time assertion that ControlBackend satisfies control.Backend.
var _ control.Backend = (*ControlBackend)(nil)
