// Package supervisor state persistence structures and functions.
package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/procstat"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// ServiceStateSnapshot is the JSON-serializable snapshot of a running service.
type ServiceStateSnapshot struct {
	Name       string    `json:"name"`
	Status     Status    `json:"status"`
	PID        int       `json:"pid"`
	PGID       int       `json:"pgid"`
	ExitCode   int       `json:"exit_code"`
	Restarts   int       `json:"restarts"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// TaskStateSnapshot is the JSON-serializable snapshot of a task's
// most recent run (or in-progress run). Tasks are one-shot so only the
// latest state is persisted — matching how ServiceStateSnapshot records the
// current spawn.
type TaskStateSnapshot struct {
	Name       string    `json:"name"`
	Command    string    `json:"command,omitempty"`
	Status     Status    `json:"status"`
	PID        int       `json:"pid"`
	PGID       int       `json:"pgid"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// SupervisorStateSnapshot is the JSON-serializable snapshot of all services in a supervisor.
type SupervisorStateSnapshot struct {
	Services map[string]ServiceStateSnapshot `json:"services"`
	Tasks    map[string]TaskStateSnapshot    `json:"tasks,omitempty"`
	// Selected is the lazy-start selection (see Options.Selected) persisted
	// so a project materialized via `start <service>` is not expanded to
	// every service by autostart after a daemon restart. Empty means all
	// services were selected.
	Selected []string  `json:"selected,omitempty"`
	SavedAt  time.Time `json:"saved_at"`
}

// StateFilePath returns the path to state.json for a project.
func StateFilePath(locs *project.Locations) string {
	return filepath.Join(locs.State, "state.json")
}

// SaveState serializes the current supervisor state to state.json in the project state directory.
// It is safe to call concurrently from run-loop goroutines and from the
// daemon's state flush; writes are serialized so concurrent transitions never
// interleave in the temporary file.
func (s *Supervisor) SaveState() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	services := make(map[string]ServiceStateSnapshot, len(s.services))
	for name, rt := range s.services {
		rt.mu.Lock()
		services[name] = ServiceStateSnapshot{
			Name:       rt.name,
			Status:     rt.status,
			PID:        rt.pid,
			PGID:       rt.pgid,
			ExitCode:   rt.exitCode,
			Restarts:   rt.restarts,
			StartedAt:  rt.startedAt,
			FinishedAt: rt.finishedAt,
		}
		rt.mu.Unlock()
	}

	tasks := make(map[string]TaskStateSnapshot, len(s.taskRuntimes))
	for name, rt := range s.taskRuntimes {
		rt.mu.Lock()
		tasks[name] = TaskStateSnapshot{
			Name:       rt.name,
			Command:    rt.command,
			Status:     rt.status,
			PID:        rt.pid,
			PGID:       rt.pgid,
			ExitCode:   rt.exitCode,
			StartedAt:  rt.startedAt,
			FinishedAt: rt.finishedAt,
		}
		rt.mu.Unlock()
	}

	selected := make([]string, 0, len(s.selected))
	for name := range s.selected {
		selected = append(selected, name)
	}
	sort.Strings(selected)

	snap := SupervisorStateSnapshot{
		Services: services,
		Tasks:    tasks,
		Selected: selected,
		SavedAt:  time.Now(),
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal supervisor state: %w", err)
	}

	statePath := StateFilePath(s.opts.Locations)
	tmpPath := statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := os.Rename(tmpPath, statePath); err != nil {
		return fmt.Errorf("rename temporary state file: %w", err)
	}
	return nil
}

// LoadState reads state.json from the project state directory if it exists.
func LoadState(locs *project.Locations) (*SupervisorStateSnapshot, error) {
	statePath := StateFilePath(locs)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read supervisor state: %w", err)
	}

	var snap SupervisorStateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal supervisor state: %w", err)
	}
	return &snap, nil
}

// RemoveState removes state.json for a project.
func RemoveState(locs *project.Locations) error {
	statePath := StateFilePath(locs)
	err := os.Remove(statePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsProcessGroupAlive checks if a process group is alive and accessible.
func IsProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	// kill(-pgid, 0) checks if any process in process group pgid exists and can be signaled
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// adoptPollInterval is how often adoptService re-checks the adopted leader's
// liveness.
const adoptPollInterval = 500 * time.Millisecond

// adoptableSnapshot reports whether a persisted snapshot describes a process
// group this supervisor can safely re-attach to: a plausible status, a live
// group leader whose current process group still matches the recorded one,
// and a leader that is not a zombie. This guards against stale snapshots that
// claim running for a process which has since exited and whose pgid was
// recycled by an unrelated process — the "ghost adoption" that would leave a
// service showing running while nothing actually runs.
func adoptableSnapshot(snap ServiceStateSnapshot) bool {
	if snap.PID <= 0 || snap.PGID <= 0 {
		return false
	}
	switch snap.Status {
	case StatusStarting, StatusRunning, StatusBackoff:
	default:
		return false
	}
	info, ok := procstat.InspectProcess(snap.PID)
	if !ok || info.Zombie {
		return false
	}
	return info.PGID == snap.PGID
}

// adoptedLeaderAlive reports whether the adopted service's original group
// leader is still alive, still leads the recorded process group, and is not a
// zombie. A leader that has exited (even with lingering group children) or
// whose pid was recycled means the adopted run is over — the same semantics a
// fresh launch gets from cmd.Wait().
func adoptedLeaderAlive(snap ServiceStateSnapshot) bool {
	if snap.PID <= 0 || snap.PGID <= 0 {
		return false
	}
	info, ok := procstat.InspectProcess(snap.PID)
	return ok && !info.Zombie && info.PGID == snap.PGID
}

// adoptableTaskSnapshot reports whether a persisted task snapshot
// describes a process group this supervisor can safely re-attach to: the
// recorded group leader must be alive, not a zombie, and still lead the
// recorded process group (tasks run with Setpgid, so the leader's pgid
// equals its pid). Anything else is a stale record of a run whose daemon is
// gone.
func adoptableTaskSnapshot(snap TaskStateSnapshot) bool {
	if snap.PID <= 0 || snap.PGID <= 0 {
		return false
	}
	info, ok := procstat.InspectProcess(snap.PID)
	return ok && !info.Zombie && info.PGID == snap.PGID
}
