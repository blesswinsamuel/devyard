package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// PortValue is a fixed port number or "auto" (allocated by devyard).
type PortValue struct {
	Number int
	Auto   bool
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (p *PortValue) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: port must be a number or \"auto\"", value.Line)
	}
	if value.Value == "auto" {
		*p = PortValue{Auto: true}
		return nil
	}
	n, err := strconv.Atoi(value.Value)
	if err != nil {
		return fmt.Errorf("line %d: port must be a number or \"auto\", got %q", value.Line, value.Value)
	}
	if err := validatePortNumber(n); err != nil {
		return fmt.Errorf("line %d: %w", value.Line, err)
	}
	*p = PortValue{Number: n}
	return nil
}

// Port is one named port of a service. The unnamed port (from `port:`) has
// an empty name.
type Port struct {
	Name  string
	Value PortValue
}

// PortMap is the ordered `ports:` map. The first entry is the default port.
type PortMap []Port

// UnmarshalYAML implements yaml.Unmarshaler.
func (m *PortMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: ports must be a map of name: port", value.Line)
	}
	seen := make(map[string]bool, len(value.Content)/2)
	out := make(PortMap, 0, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		name := value.Content[i].Value
		if err := validateHostLabel(name); err != nil {
			return fmt.Errorf("line %d: port name: %w", value.Content[i].Line, err)
		}
		if seen[name] {
			return fmt.Errorf("line %d: duplicate port name %q", value.Content[i].Line, name)
		}
		seen[name] = true
		var v PortValue
		if err := value.Content[i+1].Decode(&v); err != nil {
			return err
		}
		out = append(out, Port{Name: name, Value: v})
	}
	*m = out
	return nil
}

// PortList returns the service's ports in order; the first is the default.
func (s *Service) PortList() []Port {
	if s.Port != nil {
		return []Port{{Value: *s.Port}}
	}
	return s.Ports
}

// PortAssignments records the ports allocated for `auto` ports, keyed by
// "<service>" for the unnamed port and "<service>.<port>" for named ones.
// They are persisted so a service keeps its port across restarts.
type PortAssignments map[string]int

func assignmentKey(service, port string) string {
	if port == "" {
		return service
	}
	return service + "." + port
}

// ReadPortAssignments reads assignments from path; a missing file yields an
// empty set.
func ReadPortAssignments(path string) (PortAssignments, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return PortAssignments{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read port assignments: %w", err)
	}
	a := PortAssignments{}
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return a, nil
}

// Save writes the assignments to path atomically.
func (a PortAssignments) Save(path string) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("config: write port assignments: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: write port assignments: %w", err)
	}
	return nil
}

// FreePort asks the kernel for a free loopback port.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("config: allocate port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func validatePortNumber(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}
