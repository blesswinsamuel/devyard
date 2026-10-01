package engine

import (
	"context"
	"fmt"
	"sync"
)

// hub holds the latest published state of every service of one project and
// lets dependents wait for readiness without polling.
type hub struct {
	mu      sync.Mutex
	states  map[string]ServiceState
	changed chan struct{}
}

func newHub() *hub {
	return &hub{
		states:  make(map[string]ServiceState),
		changed: make(chan struct{}),
	}
}

func (h *hub) set(name string, st ServiceState) {
	h.mu.Lock()
	h.states[name] = st
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
}

func (h *hub) remove(name string) {
	h.mu.Lock()
	delete(h.states, name)
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
}

func (h *hub) snapshot() map[string]ServiceState {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := make(map[string]ServiceState, len(h.states))
	for k, v := range h.states {
		st[k] = v
	}
	return st
}

// errDepFailed marks a dependency that can no longer become ready.
type errDepFailed struct{ msg string }

func (e errDepFailed) Error() string { return e.msg }

// waitReady blocks until dep is ready: running and, when it has a readiness
// probe, healthy (or exited with code 0). It returns an error when the
// dependency failed in a way that won't resolve by waiting (it exited with
// an error or failed). Stopped and unhealthy dependencies are waited for.
func (h *hub) waitReady(ctx context.Context, dep string) error {
	for {
		h.mu.Lock()
		st, ok := h.states[dep]
		changed := h.changed
		h.mu.Unlock()
		if ok {
			ready, err := readiness(dep, st)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func readiness(dep string, st ServiceState) (bool, error) {
	switch st.Status {
	case StatusRunning:
		// An unhealthy dependency may still recover (slow boot, transient
		// failure): keep waiting. Only an exit or failure ends the wait.
		return st.Health == HealthNone || st.Health == HealthHealthy, nil
	case StatusExited:
		if st.ExitCode == 0 {
			return true, nil
		}
		return false, errDepFailed{fmt.Sprintf("dependency %s exited with code %d", dep, st.ExitCode)}
	case StatusFailed:
		return false, errDepFailed{fmt.Sprintf("dependency %s failed: %s", dep, st.Message)}
	}
	return false, nil
}
