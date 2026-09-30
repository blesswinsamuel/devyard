package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blesswinsamuel/devyard/internal/paths"
)

// Registration is the persisted record of a project: where its config lives,
// how to build its environment, and what the user wants running. It is the
// only persisted project state and is written only by the project's actor.
type Registration struct {
	ID         string `json:"id"`
	ConfigPath string `json:"config_path"`
	// EnvFile is the explicit env file, or "" for the default .env next to
	// the config.
	EnvFile string `json:"env_file,omitempty"`
	// Env is the launch environment captured from the shell that
	// registered or last started the project.
	Env []string `json:"env,omitempty"`
	// Desired is running | stopped | partial.
	Desired string `json:"desired"`
	// Selected lists the services wanted when Desired is partial.
	Selected  []string  `json:"selected,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// dropEnv lists launch-environment variables that describe the caller's
// shell rather than the environment services should run in.
var dropEnv = map[string]bool{"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true}

// CleanEnv filters a captured environment.
func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" || dropEnv[k] {
			continue
		}
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}

func loadRegistration(dirs paths.Dirs, id string) (*Registration, error) {
	pd, err := dirs.Project(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(pd.File())
	if err != nil {
		return nil, err
	}
	var reg Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("engine: parse %s: %w", pd.File(), err)
	}
	if reg.ID != id {
		return nil, fmt.Errorf("engine: %s: id mismatch %q", pd.File(), reg.ID)
	}
	return &reg, nil
}

func saveRegistration(dirs paths.Dirs, reg *Registration) error {
	pd, err := dirs.Project(reg.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pd.Root, 0o755); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	reg.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp := pd.File() + ".tmp"
	// 0600: the captured environment may contain secrets.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("engine: write registration: %w", err)
	}
	if err := os.Rename(tmp, pd.File()); err != nil {
		return fmt.Errorf("engine: write registration: %w", err)
	}
	return nil
}

// listRegistrations returns the ids of all registered projects.
func listRegistrations(dirs paths.Dirs) ([]string, error) {
	entries, err := os.ReadDir(dirs.ProjectsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() || paths.ValidateID(e.Name()) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dirs.ProjectsDir(), e.Name(), "project.json")); err == nil {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func deleteProjectDir(dirs paths.Dirs, id string) error {
	pd, err := dirs.Project(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(pd.Root)
}
