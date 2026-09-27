package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/daemon"
	"github.com/blesswinsamuel/devyard/internal/dag"
	"github.com/blesswinsamuel/devyard/internal/project"
)

// loadedConfig bundles everything the CLI commands need after parsing the
// config file: the file itself, the resolved project name, the config's base
// directory (for relative working_dir resolution), the topological start
// order derived from depends_on, and the env file variables used for
// interpolation and child processes.
type loadedConfig struct {
	File       *config.File
	Project    string
	BaseDir    string
	ConfigPath string
	Order      []string
	EnvFile    string
	DotEnv     map[string]string
}

func resolveProjectNameWith(configPath, projectName, envFilePath string) (string, error) {
	cfg, err := loadConfigWith(configPath, projectName, envFilePath)
	if err != nil {
		if projectName != "" {
			return projectName, nil
		}
		return "", err
	}
	return cfg.Project, nil
}

func loadConfigWith(configPath, projectName, envFilePath string) (*loadedConfig, error) {
	if configPath == "" {
		if projectName != "" {
			if regPath, err := project.GetRegisteredConfigPath(projectName); err == nil {
				if _, statErr := os.Stat(regPath); statErr == nil {
					configPath = regPath
				}
			}
		}
		if configPath == "" {
			var err error
			configPath, err = config.FindConfig(cwd())
			if err != nil {
				return nil, fmt.Errorf("devyard.yml not found; pass -f <path>: %w", err)
			}
		}
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	envFile, dotenv, err := config.ResolveDotEnv(abs, envFilePath)
	if err != nil {
		return nil, err
	}
	file, err := config.LoadWithEnv(abs, dotenv)
	if err != nil {
		return nil, err
	}

	// The daemon registers projects by the config's declared name; -p is only
	// a lookup hint for the state dir, never a rename.
	name := file.Name

	order, err := startOrder(file)
	if err != nil {
		return nil, err
	}

	return &loadedConfig{
		File:       file,
		Project:    name,
		BaseDir:    filepath.Dir(abs),
		ConfigPath: abs,
		Order:      order,
		EnvFile:    envFile,
		DotEnv:     dotenv,
	}, nil
}

// startOrder builds a dependency graph from the services' depends_on edges and
// returns the topological order (dependencies first).
func startOrder(file *config.File) ([]string, error) {
	deps := make(map[string][]string, len(file.Services))
	for name, svc := range file.Services {
		deps[name] = append([]string{}, svc.DependsOn.Order...)
	}
	g, err := dag.New(deps)
	if err != nil {
		return nil, err
	}
	order, err := g.Order()
	if err != nil {
		return nil, err
	}
	return order, nil
}

// daemonSocketPath returns the global daemon's control socket path.
func daemonSocketPath() (string, error) {
	locs, err := project.ResolveDaemon()
	if err != nil {
		return "", err
	}
	return locs.Socket, nil
}

func ensureDaemonTo(w io.Writer) (string, error) {
	locs, err := project.ResolveDaemon()
	if err != nil {
		return "", err
	}
	pid, err := daemon.DaemonRunning(locs)
	if err != nil {
		return "", fmt.Errorf("check running daemon: %w", err)
	}
	if pid == 0 {
		pid, err = daemon.SpawnDaemon(locs)
		if err != nil {
			return "", err
		}
		if w != nil {
			_, _ = fmt.Fprintf(w, "devyard: daemon started (pid %d)\n", pid)
		}
		if err := waitForDaemonStartup(locs, pid, 3*time.Second); err != nil {
			return "", err
		}
	}
	return locs.Socket, nil
}

func waitForDaemonStartup(locs *project.DaemonLocations, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := control.Dial(locs.Socket)
		if err == nil {
			_, err := c.DaemonStatus()
			_ = c.Close()
			if err == nil {
				return nil
			}
		}
		if !daemon.IsAlive(pid) {
			if errMsg := readLastDaemonError(locs.LogFile); errMsg != "" {
				return fmt.Errorf("daemon failed to start: %s", errMsg)
			}
			return fmt.Errorf("daemon (pid %d) exited unexpectedly during startup", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if errMsg := readLastDaemonError(locs.LogFile); errMsg != "" {
		return fmt.Errorf("daemon failed to start: %s", errMsg)
	}
	return fmt.Errorf("timed out waiting for daemon (pid %d) socket to become ready", pid)
}

func readLastDaemonError(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if idx := strings.Index(line, `level=ERROR`); idx >= 0 {
			if errIdx := strings.Index(line, `error="`); errIdx >= 0 {
				errPart := line[errIdx+len(`error="`):]
				if endIdx := strings.LastIndex(errPart, `"`); endIdx >= 0 {
					return errPart[:endIdx]
				}
				return errPart
			}
			return line[idx:]
		}
		if strings.HasPrefix(line, "Error:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
		}
	}
	return ""
}

// dialDaemon dials the global daemon's control socket. It returns an error if
// the daemon is not running. Used by `ps`, `logs`, `restart`, `down` which
// require the daemon to be already running.
func dialDaemon() (string, error) {
	sock, err := daemonSocketPath()
	if err != nil {
		return "", err
	}
	if !daemon.IsSocketResponsive(sock, 200*time.Millisecond) {
		return "", fmt.Errorf("no daemon running; use `devyard start` to start")
	}
	return sock, nil
}

// cwd returns the current working directory, falling back to "." on error.
func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}
