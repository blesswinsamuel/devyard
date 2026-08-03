package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/control"
	"github.com/blesswinsamuel/local-compose/internal/daemon"
	"github.com/blesswinsamuel/local-compose/internal/dag"
	"github.com/blesswinsamuel/local-compose/internal/project"
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

// flagConfigPath and flagProject are bound to the root command's persistent
// --file/-f and --project/-p flags. They're package-level so command RunE
// closures can read them without threading state through cobra.
var flagConfigPath string
var flagProject string
var flagEnvFile string

// resolveProjectName returns the project name to operate on. If projectName
// (from -p/--project) is non-empty, it is returned immediately without looking
// for local-compose.yml. Otherwise, it falls back to loadConfig to derive the
// project name from local-compose.yml in cwd.
func resolveProjectName(configPath, projectName string) (string, error) {
	if projectName != "" {
		return projectName, nil
	}
	cfg, err := loadConfig(configPath, projectName)
	if err != nil {
		return "", err
	}
	return cfg.Project, nil
}

// loadConfig finds, parses, and validates the config file, derives the project
// name, and computes the topological start order. If configPath is empty it
// walks up from the cwd looking for local-compose.yml.
func loadConfig(configPath, projectName string) (*loadedConfig, error) {
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
				return nil, fmt.Errorf("local-compose.yml not found; pass -f <path>: %w", err)
			}
		}
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	envFile, dotenv, err := config.ResolveDotEnv(abs, flagEnvFile)
	if err != nil {
		return nil, err
	}
	file, err := config.LoadWithEnv(abs, dotenv)
	if err != nil {
		return nil, err
	}

	name := projectName
	if name == "" {
		name = file.Name
	}

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

// ensureDaemon checks whether the global daemon is running and spawns it if
// not. It returns the daemon's control socket path. Used by `up` which needs
// the daemon to start a project.
func ensureDaemon() (string, error) {
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
		fmt.Fprintf(os.Stderr, "local-compose: daemon started (pid %d)\n", pid)
		_ = control.WaitForSocket(locs.Socket, 3*time.Second)
	}
	return locs.Socket, nil
}

// dialDaemon dials the global daemon's control socket. It returns an error if
// the daemon is not running. Used by `ps`, `logs`, `restart`, `down` which
// require the daemon to be already running.
func dialDaemon() (string, error) {
	sock, err := daemonSocketPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(sock); err != nil {
		return "", fmt.Errorf("no daemon running; use `local-compose up` to start")
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
