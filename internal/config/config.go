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
	RestartNo        RestartPolicy = "no"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
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

// ServicePort is one proxied port of a service.
type ServicePort struct {
	// Name is the optional port name. The unnamed (empty) entry or the first
	// entry of the ports map is the service's default port.
	Name string
	Port int
}

// Ports holds the ports a service exposes for the daemon's reverse proxy.
// It accepts either a scalar (`port: 3000`, shorthand for one unnamed
// default port) or an ordered map of named ports (`ports: {http: 3000}`).
// The first entry is the service's default port.
type Ports struct {
	Entries []ServicePort
}

// UnmarshalYAML implements yaml.Unmarshaler for Ports.
func (p *Ports) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var n int
		if err := value.Decode(&n); err != nil {
			return fmt.Errorf("port must be a number")
		}
		if err := validatePortNumber(n); err != nil {
			return err
		}
		p.Entries = []ServicePort{{Name: "", Port: n}}
		return nil
	case yaml.MappingNode:
		seen := make(map[string]bool, len(value.Content)/2)
		for i := 0; i+1 < len(value.Content); i += 2 {
			name := value.Content[i].Value
			if err := validateHostLabel(name); err != nil {
				return err
			}
			if seen[name] {
				return fmt.Errorf("duplicate port name %q", name)
			}
			seen[name] = true
			var n int
			if err := value.Content[i+1].Decode(&n); err != nil {
				return fmt.Errorf("port %q: must be a number", name)
			}
			if err := validatePortNumber(n); err != nil {
				return fmt.Errorf("port %q: %w", name, err)
			}
			p.Entries = append(p.Entries, ServicePort{Name: name, Port: n})
		}
		if len(p.Entries) == 0 {
			return fmt.Errorf("ports must not be empty")
		}
		return nil
	default:
		return fmt.Errorf("ports must be a number or a map of name: port")
	}
}

// Default returns the service's default port (the first entry).
func (p *Ports) Default() (ServicePort, bool) {
	if p == nil || len(p.Entries) == 0 {
		return ServicePort{}, false
	}
	return p.Entries[0], true
}

// ServiceProxy customizes how the daemon's reverse proxy exposes a service.
type ServiceProxy struct {
	// Host overrides the service's host label in the routed hostname
	// (<host>.<project>.<domain>). Defaults to the service name.
	Host string `yaml:"host,omitempty"`
}

// ProjectProxy configures the daemon's reverse proxy at the project level.
type ProjectProxy struct {
	// DefaultService names the service reached at <project>.<domain>. It
	// must reference a service that exposes ports.
	DefaultService string `yaml:"default_service,omitempty"`
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
	Port        int               `yaml:"port,omitempty"`
	Ports       *Ports            `yaml:"ports,omitempty"`
	Proxy       *ServiceProxy     `yaml:"proxy,omitempty"`
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

// TaskSpec describes a one-off task or command.
type TaskSpec struct {
	Command    string            `yaml:"command"`
	WorkingDir string            `yaml:"working_dir,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	Shell      string            `yaml:"shell,omitempty"`
	TTY        bool              `yaml:"tty,omitempty"`
	DependsOn  DependsOn         `yaml:"depends_on,omitempty"`
}

// Task accepts either a command string or a TaskSpec object in YAML.
type Task struct {
	Spec TaskSpec
}

// UnmarshalYAML implements yaml.Unmarshaler for Task.
func (t *Task) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var cmd string
		if err := value.Decode(&cmd); err != nil {
			return err
		}
		t.Spec = TaskSpec{Command: cmd, Shell: DefaultShell}
		return nil
	case yaml.MappingNode:
		var spec TaskSpec
		if err := value.Decode(&spec); err != nil {
			return err
		}
		if strings.TrimSpace(spec.Command) == "" {
			return fmt.Errorf("task.command is required")
		}
		if spec.Shell == "" {
			spec.Shell = DefaultShell
		}
		t.Spec = spec
		return nil
	default:
		return fmt.Errorf("task must be a string or object")
	}
}

// File is the top-level local-compose.yml schema.
type File struct {
	Version  string             `yaml:"version"`
	Name     string             `yaml:"name,omitempty"`
	Proxy    *ProjectProxy      `yaml:"proxy,omitempty"`
	Services map[string]Service `yaml:"services"`
	Tasks    map[string]Task    `yaml:"tasks,omitempty"`
}

// Load reads and validates a config file. Environment variable references in
// the file (${VAR}, ${VAR:-default}) are interpolated from the process
// environment before parsing. See Interpolate.
func Load(path string) (*File, error) {
	return LoadWithEnv(path, nil)
}

// LoadWithEnv reads and validates a config file, interpolating environment
// variable references from dotenv overlaid on the process environment (the
// process environment wins for duplicate keys). dotenv is typically the
// variables read from a project .env file.
func LoadWithEnv(path string, dotenv map[string]string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	text := Interpolate(string(data), interpolateEnv(dotenv), func(msg string) {
		fmt.Fprintf(os.Stderr, "local-compose: warning: %s\n", msg)
	})
	var file File
	if err := yaml.Unmarshal([]byte(text), &file); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := file.Validate(path); err != nil {
		return nil, err
	}
	return &file, nil
}

// interpolateEnv merges dotenv vars with the process environment for
// interpolation, with the process environment taking precedence.
func interpolateEnv(dotenv map[string]string) map[string]string {
	env := make(map[string]string, len(dotenv))
	for k, v := range dotenv {
		env[k] = v
	}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return env
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
		if svc.Port != 0 && svc.Ports != nil {
			return fmt.Errorf("service %q: port and ports are mutually exclusive", name)
		}
		if svc.Port != 0 {
			if err := validatePortNumber(svc.Port); err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			svc.Ports = &Ports{Entries: []ServicePort{{Name: "", Port: svc.Port}}}
		}
		if svc.Ports != nil {
			for _, entry := range svc.Ports.Entries {
				if err := validatePortNumber(entry.Port); err != nil {
					return fmt.Errorf("service %q: %w", name, err)
				}
				if entry.Name != "" {
					if err := validateHostLabel(entry.Name); err != nil {
						return fmt.Errorf("service %q: port %q: %w", name, entry.Name, err)
					}
				}
			}
		}
		if svc.Proxy != nil && svc.Proxy.Host != "" {
			if err := validateHostLabel(svc.Proxy.Host); err != nil {
				return fmt.Errorf("service %q: proxy.host: %w", name, err)
			}
		}
		f.Services[name] = svc
	}
	if f.Proxy != nil && f.Proxy.DefaultService != "" {
		svc, ok := f.Services[f.Proxy.DefaultService]
		if !ok {
			return fmt.Errorf("proxy.default_service references unknown service %q", f.Proxy.DefaultService)
		}
		if svc.Ports == nil || len(svc.Ports.Entries) == 0 {
			return fmt.Errorf("proxy.default_service %q exposes no ports (add port or ports)", f.Proxy.DefaultService)
		}
	}
	for name, task := range f.Tasks {
		if strings.TrimSpace(task.Spec.Command) == "" {
			return fmt.Errorf("task %q: command is required", name)
		}
		if task.Spec.Shell == "" {
			task.Spec.Shell = DefaultShell
		}
		for depName, entry := range task.Spec.DependsOn.Entries {
			if _, ok := f.Services[depName]; !ok {
				return fmt.Errorf("task %q: depends_on references unknown service %q", name, depName)
			}
			if err := validateCondition(entry.Condition); err != nil {
				return fmt.Errorf("task %q depends_on %q: %w", name, depName, err)
			}
			if entry.Condition == ConditionServiceHealthy {
				dep := f.Services[depName]
				if dep.Healthcheck == nil {
					return fmt.Errorf("task %q depends_on %q with service_healthy but %q has no healthcheck", name, depName, depName)
				}
			}
		}
		if task.Spec.Env != nil {
			for k := range task.Spec.Env {
				if strings.TrimSpace(k) == "" {
					return fmt.Errorf("task %q: env key must be non-empty", name)
				}
			}
		}
		f.Tasks[name] = task
	}
	return nil
}

func defaultProjectName(configPath string) string {
	dir := filepath.Dir(configPath)
	return filepath.Base(dir)
}

func validateRestartPolicy(p RestartPolicy) error {
	switch p {
	case RestartNo, RestartOnFailure, RestartAlways:
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

func validatePortNumber(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

func validateHostLabel(name string) error {
	if name == "" {
		return fmt.Errorf("must be non-empty")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("invalid label %q: leading or trailing dash", name)
			}
		default:
			return fmt.Errorf("invalid label %q: must be lowercase letters, digits, and dashes", name)
		}
	}
	return nil
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

// BuildEnvOver returns base with svcEnv overlaid (additive, matching the
// service env rule). Keys present in svcEnv replace the corresponding base
// keys; new keys are appended. base is typically os.Environ() layered with
// dotenv vars via BaseEnv.
func BuildEnvOver(base []string, svcEnv map[string]string) []string {
	if len(svcEnv) == 0 {
		return base
	}
	seen := make(map[string]bool, len(svcEnv))
	out := make([]string, 0, len(base)+len(svcEnv))
	for _, kv := range base {
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

// BaseEnv returns an environment slice built from the process environment
// with dotenv vars overlaid (dotenv wins for duplicate keys). Service and
// build env is layered on top via BuildEnvOver.
func BaseEnv(dotenv map[string]string) []string {
	merged := make(map[string]string, len(dotenv))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range dotenv {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}
