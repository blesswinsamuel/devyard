package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/blesswinsamuel/devyard/internal/paths"
)

// File names.
const (
	FileName      = "devyard.yml"
	LocalFileName = "devyard.local.yml"
)

// DefaultEnvFiles are loaded when a project does not set env_files.
var DefaultEnvFiles = []string{".env", ".env.local"}

// RestartPolicy controls how a service is restarted after it exits.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// Defaults.
const (
	DefaultStopTimeout = 10 * time.Second
	DefaultInterval    = 2 * time.Second
	DefaultTimeout     = 2 * time.Second
	DefaultRetries     = 30
)

// File is the devyard.yml schema. Field comments are user-facing: they
// become the descriptions in the generated JSON Schema.
type File struct {
	// Project name; defaults to the directory name. The project id is its
	// slug.
	Name string `yaml:"name,omitempty"`
	// Service served at <project>.<domain> and opened by the dashboard.
	Primary string `yaml:"primary,omitempty"`
	// Env files loaded in order, later files winning; missing files are
	// skipped. Default: [.env, .env.local].
	EnvFiles []string `yaml:"env_files,omitempty"`
	// Variables for every service and task.
	Env map[string]string `yaml:"env,omitempty"`
	// Named URLs shown with the project, in order.
	Links Links `yaml:"links,omitempty"`
	// Long-running processes. A string or list is shorthand for run.
	Services map[string]*Service `yaml:"services,omitempty"`
	// On-demand commands. A string or list is shorthand for run.
	Tasks map[string]*Task `yaml:"tasks,omitempty"`
}

// Service is a long-running process. A string or list is shorthand for run.
type Service struct {
	// Command to run: a string runs with `sh -c`, a list is executed
	// directly.
	Run Command `yaml:"run" jsonschema:"required"`
	// Working directory, relative to the project directory.
	Dir string `yaml:"dir,omitempty"`
	// Variables for this service; they win over env files.
	Env map[string]string `yaml:"env,omitempty"`
	// Env files for this service, loaded after the project's.
	EnvFiles []string `yaml:"env_files,omitempty"`
	// Services to wait for: until they are ready (or exited with code 0).
	DependsOn []string `yaml:"depends_on,omitempty"`
	// Readiness probe; without one the service is ready once started.
	Ready *Ready `yaml:"ready,omitempty"`
	// When to restart after an exit. Default: on-failure.
	Restart RestartPolicy `yaml:"restart,omitempty"`
	// Build step run before start: with --build, or when its sources
	// changed.
	Build *Build `yaml:"build,omitempty"`
	// Run in a pseudo-terminal (colors, progress bars, attach).
	TTY bool `yaml:"tty,omitempty"`
	// Port the service listens on, or "auto". Exported as $PORT.
	Port *PortValue `yaml:"port,omitempty"`
	// Named ports (number or "auto"); the first is the default port.
	// Exported as $PORT and $PORT_<NAME>.
	Ports PortMap `yaml:"ports,omitempty"`
	// Proxy host label; defaults to the service name.
	Host string `yaml:"host,omitempty"`
	// How the service is stopped.
	Stop Stop `yaml:"stop,omitempty"`
	// Start with the project. When false, the service starts only when
	// named (or needed by another service). Default: true.
	Autostart *bool `yaml:"autostart,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *Service) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return value.Decode(&s.Run)
	}
	type plain Service
	return value.Decode((*plain)(s))
}

// Starts reports whether the service starts with its project.
func (s *Service) Starts() bool { return s.Autostart == nil || *s.Autostart }

// Ready configures the readiness probe: exactly one of http, tcp and exec.
type Ready struct {
	// GET request to the service; ready on a 2xx or 3xx (or the expected
	// status).
	HTTP *HTTPProbe `yaml:"http,omitempty"`
	// TCP connection to the service.
	TCP *TCPProbe `yaml:"tcp,omitempty"`
	// Command that exits 0 when the service is ready.
	Exec *Command `yaml:"exec,omitempty"`
	// Time between probes. Default: 2s.
	Interval time.Duration `yaml:"interval,omitempty"`
	// Time limit of one probe. Default: 2s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
	// Consecutive failures that mark the service unhealthy. Default: 30.
	Retries int `yaml:"retries,omitempty"`
	// Grace period after start during which failures don't count.
	StartPeriod time.Duration `yaml:"start_period,omitempty"`
}

// HTTPProbe requests http://127.0.0.1:<port><path>.
type HTTPProbe struct {
	// Request path. Default: /.
	Path string `yaml:"path,omitempty"`
	// Port name or number. Default: the service's default port.
	Port PortRef `yaml:"port,omitempty"`
	// Expected status code. Default: any 2xx or 3xx.
	Status int `yaml:"status,omitempty"`
}

// TCPProbe connects to 127.0.0.1:<port>.
type TCPProbe struct {
	// Port name or number. Default: the service's default port.
	Port PortRef `yaml:"port,omitempty"`
}

// PortRef names a port of the service, gives a port number, or is empty for
// the service's default port.
type PortRef string

// Stop configures how a service is stopped.
type Stop struct {
	// Signal sent to stop the service. Default: SIGTERM.
	Signal string `yaml:"signal,omitempty"`
	// Wait after the signal before SIGKILL. Default: 10s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

// Build runs before the service starts. A string or list is shorthand for
// run.
type Build struct {
	// Build command: a string runs with `sh -c`, a list is executed
	// directly.
	Run Command `yaml:"run" jsonschema:"required"`
	// Working directory, relative to the project directory. Default: the
	// service's.
	Dir string `yaml:"dir,omitempty"`
	// Variables added to the service's environment for the build.
	Env map[string]string `yaml:"env,omitempty"`
	// Input files (globs relative to the project directory; ** matches
	// any directories). When set, start builds only if they changed since
	// the last successful build.
	Sources []string `yaml:"sources,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *Build) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return value.Decode(&b.Run)
	}
	type plain Build
	return value.Decode((*plain)(b))
}

// Task is an on-demand command. A string or list is shorthand for run.
type Task struct {
	// Command to run: a string runs with `sh -c`, a list is executed
	// directly. Extra arguments are appended.
	Run Command `yaml:"run" jsonschema:"required"`
	// Working directory, relative to the project directory.
	Dir string `yaml:"dir,omitempty"`
	// Variables for this task; they win over env files.
	Env map[string]string `yaml:"env,omitempty"`
	// Env files for this task, loaded after the project's.
	EnvFiles []string `yaml:"env_files,omitempty"`
	// Services to start and wait for (until ready) before running.
	DependsOn []string `yaml:"depends_on,omitempty"`
	// Run in a pseudo-terminal so prompts work. Default: true.
	TTY *bool `yaml:"tty,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (t *Task) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return value.Decode(&t.Run)
	}
	type plain Task
	return value.Decode((*plain)(t))
}

// IsTTY reports whether the task runs in a pseudo-terminal.
func (t *Task) IsTTY() bool { return t.TTY == nil || *t.TTY }

// Link is a named URL shown with the project.
type Link struct {
	Name string
	URL  string
}

// Links is the ordered `links:` map.
type Links []Link

// UnmarshalYAML implements yaml.Unmarshaler.
func (l *Links) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: links must be a map of name: url", value.Line)
	}
	out := make(Links, 0, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		k, v := value.Content[i], value.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: link %q must be a url", v.Line, k.Value)
		}
		out = append(out, Link{Name: k.Value, URL: v.Value})
	}
	*l = out
	return nil
}

// Project is a loaded config.
type Project struct {
	// Path is the absolute path of devyard.yml.
	Path string
	// Dir is the project directory; relative paths resolve against it.
	Dir  string
	ID   string
	File *File
	// DotEnv holds the merged variables of the project's env files.
	DotEnv map[string]string
	// EnvFiles lists the project env files that exist, in load order.
	EnvFiles []string
	// Inputs lists every file the config is built from, existing or not
	// (devyard.yml, devyard.local.yml and the project env files).
	Inputs   []string
	Warnings []string
}

// Load reads devyard.yml at path, overlays devyard.local.yml, loads the
// project env files and interpolates ${VAR} references from the env files
// overlaid by the launch environment.
func Load(path string, launch []string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	p := &Project{Path: abs, Dir: filepath.Dir(abs)}
	root, err := readNode(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err != nil {
		return nil, err
	}
	if root == nil {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	local := filepath.Join(p.Dir, LocalFileName)
	p.Inputs = []string{abs, local}
	overlay, err := readNode(local)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if overlay != nil {
		root = mergeNodes(root, overlay)
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse config: %s must be a map", FileName)
	}

	envFiles := DefaultEnvFiles
	if n := mapValue(root, "env_files"); n != nil {
		envFiles = nil
		if err := n.Decode(&envFiles); err != nil {
			return nil, fmt.Errorf("parse config: env_files: %w", err)
		}
	}
	for _, f := range envFiles {
		p.Inputs = append(p.Inputs, resolvePath(p.Dir, f))
	}
	p.DotEnv, p.EnvFiles, err = loadEnvFiles(p.Dir, envFiles)
	if err != nil {
		return nil, err
	}

	env := InterpolationEnv(launch, p.DotEnv)
	interpolateNode(root, env, func(msg string) { p.Warnings = append(p.Warnings, msg) })
	checkFields(root, reflect.TypeOf(File{}), "", func(msg string) { p.Warnings = append(p.Warnings, msg) })

	var file File
	if err := root.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := file.Validate(abs); err != nil {
		return nil, err
	}
	p.File = &file
	p.ID = file.ID()
	return p, nil
}

func readNode(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", filepath.Base(path), err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return doc.Content[0], nil
}

// mergeNodes overlays o onto base: maps merge key by key, anything else is
// replaced.
func mergeNodes(base, o *yaml.Node) *yaml.Node {
	if base.Kind != yaml.MappingNode || o.Kind != yaml.MappingNode {
		return o
	}
	for i := 0; i+1 < len(o.Content); i += 2 {
		key, val := o.Content[i], o.Content[i+1]
		replaced := false
		for j := 0; j+1 < len(base.Content); j += 2 {
			if base.Content[j].Value == key.Value {
				base.Content[j+1] = mergeNodes(base.Content[j+1], val)
				replaced = true
				break
			}
		}
		if !replaced {
			base.Content = append(base.Content, key, val)
		}
	}
	return base
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// interpolateNode expands ${VAR} references in every scalar value. Plain
// scalars whose text changed are re-resolved so `port: ${PORT}` decodes as
// a number.
func interpolateNode(n *yaml.Node, env map[string]string, warn func(string)) {
	switch n.Kind {
	case yaml.ScalarNode:
		v := Interpolate(n.Value, env, warn)
		if v != n.Value {
			n.Value = v
			if n.Style == 0 {
				n.Tag = ""
			}
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			interpolateNode(n.Content[i], env, warn)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			interpolateNode(c, env, warn)
		}
	case yaml.AliasNode:
	}
}

// checkFields warns about mapping keys that don't correspond to a field of
// t. It descends only where the node shape matches the type, so shorthand
// forms (a string service) are not reported.
func checkFields(n *yaml.Node, t reflect.Type, path string, warn func(string)) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Struct && n.Kind == yaml.MappingNode:
		fields := make(map[string]reflect.Type, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			ft, ok := fields[key]
			if !ok {
				warn(fmt.Sprintf("unknown field %q at %s (line %d)", key, joinPath(path, key), n.Content[i].Line))
				continue
			}
			checkFields(n.Content[i+1], ft, joinPath(path, key), warn)
		}
	case t.Kind() == reflect.Map && n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			checkFields(n.Content[i+1], t.Elem(), joinPath(path, n.Content[i].Value), warn)
		}
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// EnvMap converts a KEY=VALUE slice into a map (later entries win).
func EnvMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// InterpolationEnv merges env-file variables with the launch environment for
// ${VAR} interpolation. The launch environment wins for duplicate keys.
func InterpolationEnv(launch []string, dotenv map[string]string) map[string]string {
	env := make(map[string]string, len(launch)+len(dotenv))
	for k, v := range dotenv {
		env[k] = v
	}
	for k, v := range EnvMap(launch) {
		env[k] = v
	}
	return env
}

// ID returns the project's id: the slug of its name.
func (f *File) ID() string {
	return paths.Slugify(f.Name)
}

// Validate checks the config and applies defaults. configPath supplies the
// default project name.
func (f *File) Validate(configPath string) error {
	if f.Name == "" {
		f.Name = filepath.Base(filepath.Dir(configPath))
	}
	if f.ID() == "" {
		return fmt.Errorf("name %q does not contain any letters or digits", f.Name)
	}
	if err := validateEnv(f.Env); err != nil {
		return err
	}
	for _, l := range f.Links {
		if strings.TrimSpace(l.Name) == "" {
			return fmt.Errorf("links: name must be non-empty")
		}
		if u, err := url.Parse(l.URL); err != nil || u.Scheme == "" {
			return fmt.Errorf("links: %q: %q is not an absolute url", l.Name, l.URL)
		}
	}
	for name, svc := range f.Services {
		if svc == nil {
			return fmt.Errorf("service %q: run is required", name)
		}
		if err := f.validateService(name, svc); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
	}
	if f.Primary != "" {
		svc, ok := f.Services[f.Primary]
		if !ok {
			return fmt.Errorf("primary references unknown service %q", f.Primary)
		}
		if len(svc.PortList()) == 0 {
			return fmt.Errorf("primary service %q has no port", f.Primary)
		}
	}
	for name, task := range f.Tasks {
		if task == nil {
			return fmt.Errorf("task %q: run is required", name)
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("task names must be non-empty")
		}
		if err := f.validateTask(task); err != nil {
			return fmt.Errorf("task %q: %w", name, err)
		}
	}
	return nil
}

func (f *File) validateService(name string, svc *Service) error {
	if err := validateHostLabel(name); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if svc.Run.IsZero() {
		return fmt.Errorf("run is required")
	}
	if err := validateEnv(svc.Env); err != nil {
		return err
	}
	if err := f.validateDeps(svc.DependsOn, name); err != nil {
		return err
	}
	if svc.Restart == "" {
		svc.Restart = RestartOnFailure
	}
	switch svc.Restart {
	case RestartNever, RestartOnFailure, RestartAlways:
	default:
		return fmt.Errorf("invalid restart policy %q (expected never, on-failure or always)", svc.Restart)
	}
	if svc.Port != nil && len(svc.Ports) > 0 {
		return fmt.Errorf("port and ports are mutually exclusive")
	}
	if svc.Ports != nil && len(svc.Ports) == 0 {
		return fmt.Errorf("ports must not be empty")
	}
	if svc.Host != "" {
		if err := validateHostLabel(svc.Host); err != nil {
			return fmt.Errorf("host: %w", err)
		}
	}
	if svc.Stop.Timeout < 0 {
		return fmt.Errorf("stop.timeout must not be negative")
	}
	if svc.Stop.Timeout == 0 {
		svc.Stop.Timeout = DefaultStopTimeout
	}
	if svc.Build != nil {
		if svc.Build.Run.IsZero() {
			return fmt.Errorf("build.run is required")
		}
		if err := validateEnv(svc.Build.Env); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}
	if svc.Ready != nil {
		if err := validateReady(svc.Ready, svc.PortList()); err != nil {
			return fmt.Errorf("ready: %w", err)
		}
	}
	return f.validateRefs(svc.Run, svc.Env)
}

func (f *File) validateTask(task *Task) error {
	if task.Run.IsZero() {
		return fmt.Errorf("run is required")
	}
	if err := validateEnv(task.Env); err != nil {
		return err
	}
	if err := f.validateDeps(task.DependsOn, ""); err != nil {
		return err
	}
	return f.validateRefs(task.Run, task.Env)
}

func (f *File) validateDeps(deps []string, self string) error {
	seen := make(map[string]bool, len(deps))
	for _, d := range deps {
		if _, ok := f.Services[d]; !ok {
			return fmt.Errorf("depends_on references unknown service %q", d)
		}
		if d == self {
			return fmt.Errorf("depends_on references itself")
		}
		if seen[d] {
			return fmt.Errorf("depends_on lists %q twice", d)
		}
		seen[d] = true
	}
	return nil
}

// validateRefs checks that every ${service.field} reference in a command
// and env values names an existing port.
func (f *File) validateRefs(run Command, env map[string]string) error {
	check := func(s string) (string, error) {
		return expandRefs(s, func(svc, field string) (string, error) {
			_, err := f.refPort(svc, field)
			return "", err
		})
	}
	if _, err := run.mapStrings(check); err != nil {
		return err
	}
	for k, v := range env {
		if _, err := check(v); err != nil {
			return fmt.Errorf("env %s: %w", k, err)
		}
	}
	return nil
}

// refPort returns the port a ${service.field} reference points at.
func (f *File) refPort(service, field string) (Port, error) {
	svc, ok := f.Services[service]
	if !ok {
		return Port{}, fmt.Errorf("${%s.%s}: unknown service %q", service, field, service)
	}
	ports := svc.PortList()
	name, named := strings.CutPrefix(field, "ports.")
	switch {
	case field == "port" || field == "url":
		if len(ports) == 0 {
			return Port{}, fmt.Errorf("${%s.%s}: service %q has no port", service, field, service)
		}
		return ports[0], nil
	case named:
		for _, p := range ports {
			if p.Name == name {
				return p, nil
			}
		}
		return Port{}, fmt.Errorf("${%s.%s}: service %q has no port %q", service, field, service, name)
	}
	return Port{}, fmt.Errorf("${%s.%s}: unknown field %q (expected port, url or ports.<name>)", service, field, field)
}

func validateReady(r *Ready, ports []Port) error {
	kinds := 0
	for _, set := range []bool{r.HTTP != nil, r.TCP != nil, r.Exec != nil} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return fmt.Errorf("exactly one of http, tcp or exec is required")
	}
	if r.Interval < 0 || r.Timeout < 0 || r.Retries < 0 || r.StartPeriod < 0 {
		return fmt.Errorf("interval, timeout, retries and start_period must not be negative")
	}
	if r.Interval == 0 {
		r.Interval = DefaultInterval
	}
	if r.Timeout == 0 {
		r.Timeout = DefaultTimeout
	}
	if r.Retries == 0 {
		r.Retries = DefaultRetries
	}
	switch {
	case r.HTTP != nil:
		if r.HTTP.Path == "" {
			r.HTTP.Path = "/"
		}
		if !strings.HasPrefix(r.HTTP.Path, "/") {
			return fmt.Errorf("http.path must start with /")
		}
		if r.HTTP.Status != 0 && (r.HTTP.Status < 100 || r.HTTP.Status > 599) {
			return fmt.Errorf("http.status must be an HTTP status code")
		}
		if _, err := probePort(r.HTTP.Port, ports); err != nil {
			return fmt.Errorf("http.port: %w", err)
		}
	case r.TCP != nil:
		if _, err := probePort(r.TCP.Port, ports); err != nil {
			return fmt.Errorf("tcp.port: %w", err)
		}
	case r.Exec.IsZero():
		return fmt.Errorf("exec must not be empty")
	}
	return nil
}

// probePort resolves a probe's port reference: a number, a port name, or
// empty for the default port.
func probePort(ref PortRef, ports []Port) (Port, error) {
	if ref == "" {
		if len(ports) == 0 {
			return Port{}, fmt.Errorf("the service has no port; set one")
		}
		return ports[0], nil
	}
	if n, err := strconv.Atoi(string(ref)); err == nil {
		if err := validatePortNumber(n); err != nil {
			return Port{}, err
		}
		return Port{Value: PortValue{Number: n}}, nil
	}
	for _, p := range ports {
		if p.Name == string(ref) {
			return p, nil
		}
	}
	return Port{}, fmt.Errorf("unknown port %q", ref)
}

func validateEnv(env map[string]string) error {
	for k := range env {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "= ") {
			return fmt.Errorf("invalid env name %q", k)
		}
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
				return fmt.Errorf("invalid name %q: leading or trailing dash", name)
			}
		default:
			return fmt.Errorf("invalid name %q: must be lowercase letters, digits, and dashes", name)
		}
	}
	return nil
}

// FindConfig walks up from startDir looking for devyard.yml.
func FindConfig(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, FileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("%s not found", FileName)
}

func resolvePath(base, p string) string {
	if p == "" {
		return base
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
