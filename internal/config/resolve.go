package config

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Process is a resolved command: what to execute, where, and with which
// environment.
type Process struct {
	Cmd Command
	Dir string
	// Env is the complete environment (launch environment included).
	Env []string
	// EnvKeys lists the variables the project defines (everything but the
	// launch environment).
	EnvKeys []string
}

// ResolvedPort is a service port with its number decided.
type ResolvedPort struct {
	Name string
	Port int
	Auto bool
}

// Probe is a resolved readiness probe.
type Probe struct {
	// URL is set for http probes.
	URL string
	// Status is the expected HTTP status (0: any 2xx or 3xx).
	Status int
	// Addr is set for tcp probes.
	Addr string
	// Exec is set for exec probes.
	Exec        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

// Kind returns "http", "tcp" or "exec".
func (p *Probe) Kind() string {
	switch {
	case p.URL != "":
		return "http"
	case p.Addr != "":
		return "tcp"
	}
	return "exec"
}

// Target describes what the probe checks, for display.
func (p *Probe) Target() string {
	switch {
	case p.URL != "":
		return p.URL
	case p.Addr != "":
		return p.Addr
	}
	return Command{Argv: p.Exec}.String()
}

// ResolvedBuild is a resolved build step.
type ResolvedBuild struct {
	Process
	// Sources are absolute glob patterns.
	Sources []string
}

// ResolvedService is a service ready to launch.
type ResolvedService struct {
	Process
	Ports []ResolvedPort
	Ready *Probe
	Build *ResolvedBuild
}

// Resolved is a project with every port, path and environment decided.
type Resolved struct {
	Services map[string]*ResolvedService
	Tasks    map[string]*Process
	// Ports are the auto-port assignments in use (stale entries dropped).
	Ports PortAssignments
}

// Resolve decides ports and builds every process of the project. assigned
// holds earlier auto-port assignments, which are kept; alloc picks a port
// for new ones (FreePort when nil).
func (p *Project) Resolve(launch []string, assigned PortAssignments, alloc func() (int, error)) (*Resolved, error) {
	if alloc == nil {
		alloc = FreePort
	}
	f := p.File
	r := &Resolved{
		Services: make(map[string]*ResolvedService, len(f.Services)),
		Tasks:    make(map[string]*Process, len(f.Tasks)),
		Ports:    PortAssignments{},
	}
	used := map[int]bool{}
	for _, n := range assigned {
		used[n] = true
	}
	ports := make(map[string][]ResolvedPort, len(f.Services))
	for _, name := range sortedKeys(f.Services) {
		for _, port := range f.Services[name].PortList() {
			rp := ResolvedPort{Name: port.Name, Port: port.Value.Number, Auto: port.Value.Auto}
			if rp.Auto {
				key := assignmentKey(name, port.Name)
				if n, ok := assigned[key]; ok {
					rp.Port = n
				} else {
					for {
						n, err := alloc()
						if err != nil {
							return nil, err
						}
						if !used[n] {
							rp.Port = n
							break
						}
					}
				}
				used[rp.Port] = true
				r.Ports[key] = rp.Port
			}
			ports[name] = append(ports[name], rp)
		}
	}
	lookup := func(service, field string) (string, error) {
		port, err := f.refPort(service, field)
		if err != nil {
			return "", err
		}
		for _, rp := range ports[service] {
			if rp.Name == port.Name {
				if field == "url" {
					return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(rp.Port)), nil
				}
				return strconv.Itoa(rp.Port), nil
			}
		}
		return "", fmt.Errorf("${%s.%s}: unresolved port", service, field)
	}
	expand := func(s string) (string, error) { return expandRefs(s, lookup) }

	base := map[string]string{}
	for k, v := range p.DotEnv {
		base[k] = v
	}
	for k, v := range f.Env {
		base[k] = v
	}

	for name, svc := range f.Services {
		files, _, err := loadEnvFiles(p.Dir, svc.EnvFiles)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		portEnv := map[string]string{}
		for i, rp := range ports[name] {
			if i == 0 {
				portEnv["PORT"] = strconv.Itoa(rp.Port)
			}
			if rp.Name != "" {
				portEnv["PORT_"+envName(rp.Name)] = strconv.Itoa(rp.Port)
			}
		}
		own, err := expandEnv(svc.Env, expand)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		layers := []map[string]string{base, files, portEnv, own}
		cmd, err := svc.Run.mapStrings(expand)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		dir := resolvePath(p.Dir, svc.Dir)
		rs := &ResolvedService{
			Process: newProcess(cmd, dir, launch, layers...),
			Ports:   ports[name],
		}
		if b := svc.Build; b != nil {
			bcmd, err := b.Run.mapStrings(expand)
			if err != nil {
				return nil, fmt.Errorf("service %q: build: %w", name, err)
			}
			benv, err := expandEnv(b.Env, expand)
			if err != nil {
				return nil, fmt.Errorf("service %q: build: %w", name, err)
			}
			bdir := dir
			if b.Dir != "" {
				bdir = resolvePath(p.Dir, b.Dir)
			}
			rs.Build = &ResolvedBuild{Process: newProcess(bcmd, bdir, launch, append(layers, benv)...)}
			for _, src := range b.Sources {
				rs.Build.Sources = append(rs.Build.Sources, resolvePath(p.Dir, src))
			}
		}
		if rd := svc.Ready; rd != nil {
			probe := &Probe{Interval: rd.Interval, Timeout: rd.Timeout, Retries: rd.Retries, StartPeriod: rd.StartPeriod}
			portNumber := func(ref PortRef) int {
				port, _ := probePort(ref, svc.PortList())
				if port.Value.Auto {
					for _, rp := range ports[name] {
						if rp.Name == port.Name {
							return rp.Port
						}
					}
				}
				return port.Value.Number
			}
			switch {
			case rd.HTTP != nil:
				probe.URL = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(portNumber(rd.HTTP.Port))) + rd.HTTP.Path
				probe.Status = rd.HTTP.Status
			case rd.TCP != nil:
				probe.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(portNumber(rd.TCP.Port)))
			default:
				ecmd, err := rd.Exec.mapStrings(expand)
				if err != nil {
					return nil, fmt.Errorf("service %q: ready: %w", name, err)
				}
				probe.Exec = ecmd.Args()
			}
			rs.Ready = probe
		}
		r.Services[name] = rs
	}
	for name, task := range f.Tasks {
		files, _, err := loadEnvFiles(p.Dir, task.EnvFiles)
		if err != nil {
			return nil, fmt.Errorf("task %q: %w", name, err)
		}
		own, err := expandEnv(task.Env, expand)
		if err != nil {
			return nil, fmt.Errorf("task %q: %w", name, err)
		}
		cmd, err := task.Run.mapStrings(expand)
		if err != nil {
			return nil, fmt.Errorf("task %q: %w", name, err)
		}
		proc := newProcess(cmd, resolvePath(p.Dir, task.Dir), launch, base, files, own)
		r.Tasks[name] = &proc
	}
	return r, nil
}

// TerminalEnv is the environment of interactive terminals opened in the
// project: the launch environment with the project's env files and env.
func (p *Project) TerminalEnv(launch []string) []string {
	return newProcess(Command{}, p.Dir, launch, p.DotEnv, p.File.Env).Env
}

func newProcess(cmd Command, dir string, launch []string, layers ...map[string]string) Process {
	merged := EnvMap(launch)
	keys := map[string]bool{}
	for _, m := range layers {
		for k, v := range m {
			merged[k] = v
			keys[k] = true
		}
	}
	env := make([]string, 0, len(merged))
	for k, v := range merged {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return Process{Cmd: cmd, Dir: dir, Env: env, EnvKeys: sortedKeys(keys)}
}

func expandEnv(env map[string]string, expand func(string) (string, error)) (map[string]string, error) {
	out := make(map[string]string, len(env))
	for k, v := range env {
		s, err := expand(v)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

// envName converts a port name to an env var suffix: "hmr-ws" -> "HMR_WS".
func envName(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
