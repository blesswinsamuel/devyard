package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blesswinsamuel/local-compose/internal/config"
	"github.com/blesswinsamuel/local-compose/internal/dag"
	"github.com/blesswinsamuel/local-compose/internal/project"
)

// loadedConfig bundles everything the CLI commands need after parsing the
// config file: the file itself, the resolved project name, the config's base
// directory (for relative working_dir resolution), and the topological start
// order derived from depends_on.
type loadedConfig struct {
	File       *config.File
	Project    string
	BaseDir    string
	ConfigPath string
	Order      []string
}

// flagConfigPath and flagProject are bound to the root command's persistent
// --file/-f and --project/-p flags. They're package-level so command RunE
// closures can read them without threading state through cobra.
var flagConfigPath string
var flagProject string

// loadConfig finds, parses, and validates the config file, derives the project
// name, and computes the topological start order. If configPath is empty it
// walks up from the cwd looking for local-compose.yml.
func loadConfig(configPath, projectName string) (*loadedConfig, error) {
	if configPath == "" {
		var err error
		configPath, err = config.FindConfig(cwd())
		if err != nil {
			return nil, fmt.Errorf("local-compose.yml not found; pass -f <path>: %w", err)
		}
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	file, err := config.Load(abs)
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

// resolveLocations derives the runtime/state dirs for the resolved project.
func resolveLocations(projectName string) (*project.Locations, error) {
	return project.Resolve(projectName)
}

// cwd returns the current working directory, falling back to "." on error.
func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// waitForSocket polls until a Unix socket exists at path or the timeout
// elapses. It's used after `up -d` to give the daemonized child a moment to
// bind the control socket before subsequent commands race to dial it.
func waitForSocket(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
