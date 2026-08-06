// Package supervisor state persistence structures and functions.
package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

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

// ActionStateSnapshot is the JSON-serializable snapshot of an action's
// most recent run (or in-progress run). Actions are one-shot so only the
// latest state is persisted — matching how ServiceStateSnapshot records the
// current spawn.
type ActionStateSnapshot struct {
	Name       string    `json:"name"`
	Command    string    `json:"command,omitempty"`
	Status     Status    `json:"status"`
	PID        int       `json:"pid"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// SupervisorStateSnapshot is the JSON-serializable snapshot of all services in a supervisor.
type SupervisorStateSnapshot struct {
	Services map[string]ServiceStateSnapshot `json:"services"`
	Actions  map[string]ActionStateSnapshot  `json:"actions,omitempty"`
	SavedAt  time.Time                       `json:"saved_at"`
}

// StateFilePath returns the path to state.json for a project.
func StateFilePath(locs *project.Locations) string {
	return filepath.Join(locs.State, "state.json")
}

// SaveState serializes the current supervisor state to state.json in the project state directory.
func (s *Supervisor) SaveState() error {
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

	actions := make(map[string]ActionStateSnapshot, len(s.actionRuntimes))
	for name, rt := range s.actionRuntimes {
		rt.mu.Lock()
		actions[name] = ActionStateSnapshot{
			Name:       rt.name,
			Command:    rt.command,
			Status:     rt.status,
			PID:        rt.pid,
			ExitCode:   rt.exitCode,
			StartedAt:  rt.startedAt,
			FinishedAt: rt.finishedAt,
		}
		rt.mu.Unlock()
	}

	snap := SupervisorStateSnapshot{
		Services: services,
		Actions:  actions,
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
