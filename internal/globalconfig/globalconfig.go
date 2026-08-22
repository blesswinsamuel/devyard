// Package globalconfig loads and saves the user-level config file at
// $XDG_CONFIG_HOME/local-compose/config.yml (default
// ~/.config/local-compose/config.yml). It holds settings that apply across
// all projects, such as the web UI host/port defaults for `local-compose web`.
package globalconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/blesswinsamuel/local-compose/internal/project"
)

// DefaultHost is the default bind address for the web UI. Loopback only for
// security; users who want remote access must explicitly set host: 0.0.0.0.
const DefaultHost = "127.0.0.1"

// DefaultPort is the default port for the web UI.
const DefaultPort = 9090

// WebConfig holds default bind settings for `local-compose web`.
type WebConfig struct {
	// Host is the bind address. Defaults to 127.0.0.1 (loopback only).
	Host string `yaml:"host"`
	// Port is the TCP port. Defaults to 9090.
	Port int `yaml:"port"`
}

// Config is the top-level global config schema.
type Config struct {
	Web WebConfig `yaml:"web"`
}

// Defaults returns a Config populated with default values.
func Defaults() Config {
	return Config{
		Web: WebConfig{
			Host: DefaultHost,
			Port: DefaultPort,
		},
	}
}

// ConfigPath returns the absolute path to the global config file, using
// $XDG_CONFIG_HOME/local-compose/config.yml (default
// ~/.config/local-compose/config.yml).
func ConfigPath() (string, error) {
	return configPath(os.Getenv, homeDir)
}

// homeDir returns the user's home directory, preferring $HOME.
func homeDir() (string, error) {
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h, nil
	}
	return "", fmt.Errorf("could not determine home directory: set $HOME")
}

// envGetter is a small abstraction so resolution can be unit-tested without
// mutating the real process environment.
type envGetter func(string) string

func configPath(getenv envGetter, home func() (string, error)) (string, error) {
	if c := getenv("XDG_CONFIG_HOME"); c != "" {
		return filepath.Join(c, project.AppDir, "config.yml"), nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", project.AppDir, "config.yml"), nil
}

// Load reads the global config file. If the file does not exist, it returns
// Defaults. Unknown fields produce a warning (printed to stderr) but do not
// error, matching the convention for local-compose.yml.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return loadFile(path, os.ReadFile, os.Stderr)
}

// readerFunc is a small adapter so loadFile can be unit-tested.
type readerFunc func(string) ([]byte, error)

func loadFile(path string, read readerFunc, errOut io.Writer) (*Config, error) {
	data, err := read(path)
	if err != nil {
		if os.IsNotExist(err) {
			d := Defaults()
			return &d, nil
		}
		return nil, fmt.Errorf("read global config: %w", err)
	}

	cfg := Defaults()
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse global config: %w", err)
	}

	knownTopLevel := map[string]bool{
		"web": true,
	}

	for key := range raw {
		if !knownTopLevel[key] {
			_, _ = fmt.Fprintf(errOut, "local-compose: warning: unknown field %q in global config\n", key)
		}
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse global config: %w", err)
	}

	if cfg.Web.Host == "" {
		cfg.Web.Host = DefaultHost
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = DefaultPort
	}

	return &cfg, nil
}

// Save writes the config to disk in YAML format, creating the parent
// directory if needed.
func Save(cfg *Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return saveFile(path, cfg)
}

func saveFile(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal global config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}
	return nil
}

// LoadForTest loads a config from a specific path with a custom error writer.
// It is exported for use by tests in external test packages.
func LoadForTest(path string, errOut io.Writer) (*Config, error) {
	return loadFile(path, os.ReadFile, errOut)
}

// SaveForTest saves a config to a specific path. It is exported for use by
// tests in external test packages.
func SaveForTest(path string, cfg *Config) error {
	return saveFile(path, cfg)
}

// String returns a human-readable summary of the config (for logging).
func (c *Config) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "web=%s:%d", c.Web.Host, c.Web.Port)
	return sb.String()
}
