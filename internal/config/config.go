package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultShell = "sh"

// RestartPolicy controls how a service is restarted after exit.
type RestartPolicy string

const (
	RestartNo            RestartPolicy = "no"
	RestartOnFailure     RestartPolicy = "on-failure"
	RestartAlways        RestartPolicy = "always"
	RestartUnlessStopped RestartPolicy = "unless-stopped"
)

// DependsOnCondition is the condition for depends_on.
type DependsOnCondition string

const (
	ConditionServiceStarted DependsOnCondition = "service_started"
	ConditionServiceHealthy DependsOnCondition = "service_healthy"
)

// BuildSpec describes a pre-start build command.
type BuildSpec struct {
	Command    string            `yaml:"command"`
	WorkingDir string            `yaml:"working_dir,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	Shell      string            `yaml:"shell,omitempty"`
}

// Build accepts either a command string or a BuildSpec object in YAML.
type Build struct {
	Spec BuildSpec
}

// UnmarshalYAML implements yaml.Unmarshaler for Build.
func (b *Build) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var cmd string
		if err := value.Decode(&cmd); err != nil {
			return err
		}
		b.Spec = BuildSpec{Command: cmd, Shell: DefaultShell}
		return nil
	case yaml.MappingNode:
		var spec BuildSpec
		if err := value.Decode(&spec); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Command) == "" {
			return fmt.Errorf("build.command is required")
		}
		if spec.Shell == "" {
			spec.Shell = DefaultShell
		}
		b.Spec = spec
		return nil
	default:
		return fmt.Errorf("build must be a string or object")
	}
}

// Healthcheck configures periodic health probes.
type Healthcheck struct {
	Test     []string      `yaml:"test"`
	Interval time.Duration `yaml:"interval,omitempty"`
	Retries  int           `yaml:"retries,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty"`
}

// DependsOnEntry is the long form of depends_on.
type DependsOnEntry struct {
	Condition DependsOnCondition `yaml:"condition,omitempty"`
}

// Service defines one managed process.
type Service struct {
	Command     string            `yaml:"command"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	DependsOn   DependsOn         `yaml:"depends_on,omitempty"`
	Healthcheck *Healthcheck      `yaml:"healthcheck,omitempty"`
	Restart     RestartPolicy     `yaml:"restart,omitempty"`
	Build       *Build            `yaml:"build,omitempty"`
	Shell       string            `yaml:"shell,omitempty"`
	TTY         bool              `yaml:"tty,omitempty"`
}

// DependsOn accepts either a list of service names or a map with conditions.
type DependsOn struct {
	Entries map[string]DependsOnEntry
	Order   []string
}

// UnmarshalYAML implements yaml.Unmarshaler for DependsOn.
func (d *DependsOn) UnmarshalYAML(value *yaml.Node) error {
	d.Entries = make(map[string]DependsOnEntry)
	d.Order = nil

	switch value.Kind {
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return err
		}
		for _, name := range names {
			d.Entries[name] = DependsOnEntry{Condition: ConditionServiceStarted}
			d.Order = append(d.Order, name)
		}
		return nil
	case yaml.MappingNode:
		var raw map[string]DependsOnEntry
		if err := value.Decode(&raw); err != nil {
			return err
		}
		for name, entry := range raw {
			if entry.Condition == "" {
				entry.Condition = ConditionServiceStarted
			}
			d.Entries[name] = entry
			d.Order = append(d.Order, name)
		}
		return nil
	default:
		return fmt.Errorf("depends_on must be a list or map")
	}
}

// File is the top-level local-compose.yml schema.
type File struct {
	Version  string             `yaml:"version"`
	Name     string             `yaml:"name,omitempty"`
	Services map[string]Service `yaml:"services"`
}

// Load reads and validates a config file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var file File
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := file.Validate(path); err != nil {
		return nil, err
	}
	return &file, nil
}

// Validate checks the loaded config and applies defaults.
func (f *File) Validate(configPath string) error {
	if f.Version == "" {
		return fmt.Errorf("version is required")
	}
	if len(f.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	if f.Name == "" {
		f.Name = defaultProjectName(configPath)
	}
	for name, svc := range f.Services {
		if strings.TrimSpace(svc.Command) == "" {
			return fmt.Errorf("service %q: command is required", name)
		}
		if svc.Shell == "" {
			svc.Shell = DefaultShell
		}
		if svc.Restart == "" {
			svc.Restart = RestartNo
		}
		if err := validateRestartPolicy(svc.Restart); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		for depName, entry := range svc.DependsOn.Entries {
			if _, ok := f.Services[depName]; !ok {
				return fmt.Errorf("service %q: depends_on references unknown service %q", name, depName)
			}
			if err := validateCondition(entry.Condition); err != nil {
				return fmt.Errorf("service %q depends_on %q: %w", name, depName, err)
			}
			if entry.Condition == ConditionServiceHealthy {
				dep := f.Services[depName]
				if dep.Healthcheck == nil {
					return fmt.Errorf("service %q depends_on %q with service_healthy but %q has no healthcheck", name, depName, depName)
				}
			}
		}
		if svc.Env != nil {
			for k, v := range svc.Env {
				if strings.TrimSpace(k) == "" {
					return fmt.Errorf("service %q: env key must be non-empty", name)
				}
				_ = v
			}
		}
		if svc.Build != nil {
			if svc.Build.Spec.Shell == "" {
				svc.Build.Spec.Shell = DefaultShell
			}
		}
		if svc.Healthcheck != nil {
			if len(svc.Healthcheck.Test) == 0 {
				return fmt.Errorf("service %q: healthcheck.test is required", name)
			}
			if svc.Healthcheck.Interval == 0 {
				svc.Healthcheck.Interval = 5 * time.Second
			}
			if svc.Healthcheck.Timeout == 0 {
				svc.Healthcheck.Timeout = 2 * time.Second
			}
			if svc.Healthcheck.Retries == 0 {
				svc.Healthcheck.Retries = 3
			}
		}
		f.Services[name] = svc
	}
	return nil
}

func defaultProjectName(configPath string) string {
	dir := filepath.Dir(configPath)
	return filepath.Base(dir)
}

func validateRestartPolicy(p RestartPolicy) error {
	switch p {
	case RestartNo, RestartOnFailure, RestartAlways, RestartUnlessStopped:
		return nil
	default:
		return fmt.Errorf("invalid restart policy %q", p)
	}
}

func validateCondition(c DependsOnCondition) error {
	switch c {
	case ConditionServiceStarted, ConditionServiceHealthy:
		return nil
	default:
		return fmt.Errorf("invalid condition %q", c)
	}
}

// FindConfig walks up from cwd looking for local-compose.yml.
func FindConfig(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "local-compose.yml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("local-compose.yml not found")
}

// BuildEnv returns the parent environment with svcEnv overlaid (additive,
// matching the service env rule). Keys present in svcEnv replace the
// corresponding parent keys; new keys are appended.
func BuildEnv(svcEnv map[string]string) []string {
	parent := os.Environ()
	if len(svcEnv) == 0 {
		return parent
	}
	seen := make(map[string]bool, len(svcEnv))
	out := make([]string, 0, len(parent)+len(svcEnv))
	for _, kv := range parent {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if v, ok := svcEnv[k]; ok {
			out = append(out, k+"="+v)
			seen[k] = true
			continue
		}
		out = append(out, kv)
	}
	for k, v := range svcEnv {
		if !seen[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}
