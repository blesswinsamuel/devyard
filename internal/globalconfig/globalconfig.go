// Package globalconfig loads and saves the user-level config file at
// $XDG_CONFIG_HOME/devyard/config.yml (see paths.Dirs.GlobalConfig). It holds
// settings that apply across all projects: the list of projects (and groups
// of them), the web dashboard listener and the reverse proxy.
package globalconfig

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults.
const (
	DefaultHost              = "127.0.0.1"
	DefaultPort              = 9090
	DefaultProxyHost         = "127.0.0.1"
	DefaultProxyPort         = 8080
	DefaultProxyTLSPort      = 8443
	DefaultProxyDomainSuffix = "localhost"
)

// WebConfig configures the daemon's web dashboard listener.
type WebConfig struct {
	// Host is the bind address. Defaults to 127.0.0.1 (loopback only).
	Host string `yaml:"host"`
	// Port is the TCP port. Defaults to 9090; 0 picks an ephemeral port.
	Port int `yaml:"port"`
	// AllowedHosts are extra Host header values the dashboard accepts, in
	// addition to loopback names, "devyard" and anything under the proxy
	// domain suffix. This protects against DNS rebinding.
	AllowedHosts []string `yaml:"allowed_hosts,omitempty"`
	// PasswordHash is a bcrypt hash of the dashboard password (`devyard
	// auth set-password`). When set, the API and websocket require a
	// login cookie; the SPA serves a login screen. Empty leaves the
	// dashboard open.
	PasswordHash string `yaml:"password_hash,omitempty"`
}

// ProxyTLSConfig holds TLS settings for the reverse proxy.
type ProxyTLSConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Port         int    `yaml:"port,omitempty"`
	CertFile     string `yaml:"cert_file,omitempty"`
	KeyFile      string `yaml:"key_file,omitempty"`
	HTTPRedirect bool   `yaml:"http_redirect,omitempty"`
}

// ProxyConfig configures the reverse proxy that exposes services at named
// URLs (<service>.<project>.<domain_suffix>:<port>).
type ProxyConfig struct {
	Host string `yaml:"host"`
	// Port is the TCP port. Defaults to 8080; 0 picks an ephemeral port.
	Port         int            `yaml:"port"`
	DomainSuffix string         `yaml:"domain_suffix"`
	TLS          ProxyTLSConfig `yaml:"tls,omitempty"`
}

// EffectiveTLSPort returns the HTTPS port: TLS.Port when set, 443 when the
// HTTP port is 80, otherwise DefaultProxyTLSPort.
func (p *ProxyConfig) EffectiveTLSPort() int {
	if p.TLS.Port > 0 {
		return p.TLS.Port
	}
	if p.Port == 80 {
		return 443
	}
	return DefaultProxyTLSPort
}

// Config is the global config schema.
type Config struct {
	// Projects lists the projects devyard manages, in display order. Each
	// entry is a directory (with a devyard.yml, or without one for a
	// git-only project) or the path of a config file; `~` is expanded.
	Projects []string `yaml:"projects,omitempty"`
	// ProjectsSet reports whether the file has a `projects` key at all: an
	// absent key leaves the registered projects alone, an empty list
	// removes them.
	ProjectsSet bool `yaml:"-"`
	// Groups name sets of project ids, started and stopped together
	// (`devyard start @work`).
	Groups map[string][]string `yaml:"groups,omitempty"`
	Web    WebConfig           `yaml:"web"`
	Proxy  ProxyConfig         `yaml:"proxy"`
}

// Defaults returns the default config.
func Defaults() Config {
	return Config{
		Web:   WebConfig{Host: DefaultHost, Port: DefaultPort},
		Proxy: ProxyConfig{Host: DefaultProxyHost, Port: DefaultProxyPort, DomainSuffix: DefaultProxyDomainSuffix},
	}
}

// raw mirrors Config with pointers so an explicit `port: 0` (ephemeral) can
// be told apart from an absent port (default).
type raw struct {
	Projects *[]string           `yaml:"projects"`
	Groups   map[string][]string `yaml:"groups"`
	Web      *struct {
		Host         string   `yaml:"host"`
		Port         *int     `yaml:"port"`
		AllowedHosts []string `yaml:"allowed_hosts"`
		PasswordHash string   `yaml:"password_hash"`
	} `yaml:"web"`
	Proxy *struct {
		Host         string         `yaml:"host"`
		Port         *int           `yaml:"port"`
		DomainSuffix string         `yaml:"domain_suffix"`
		TLS          ProxyTLSConfig `yaml:"tls"`
	} `yaml:"proxy"`
}

var knownKeys = map[string]bool{"projects": true, "groups": true, "web": true, "proxy": true}

// Load reads the config at path. A missing file yields Defaults. Unknown
// top-level fields produce a warning on warn but are not an error.
func Load(path string, warn io.Writer) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			d := Defaults()
			return &d, nil
		}
		return nil, fmt.Errorf("globalconfig: read: %w", err)
	}
	return Parse(data, warn)
}

// Parse parses config file contents.
func Parse(data []byte, warn io.Writer) (*Config, error) {
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("globalconfig: parse: %w", err)
	}
	for key := range top {
		if !knownKeys[key] && warn != nil {
			_, _ = fmt.Fprintf(warn, "devyard: warning: unknown field %q in global config\n", key)
		}
	}
	var r raw
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("globalconfig: parse: %w", err)
	}
	cfg := Defaults()
	if r.Projects != nil {
		cfg.ProjectsSet = true
		cfg.Projects = *r.Projects
	}
	cfg.Groups = r.Groups
	if r.Web != nil {
		if r.Web.Host != "" {
			cfg.Web.Host = r.Web.Host
		}
		if r.Web.Port != nil {
			cfg.Web.Port = *r.Web.Port
		}
		cfg.Web.AllowedHosts = r.Web.AllowedHosts
		cfg.Web.PasswordHash = r.Web.PasswordHash
	}
	if r.Proxy != nil {
		if r.Proxy.Host != "" {
			cfg.Proxy.Host = r.Proxy.Host
		}
		if r.Proxy.Port != nil {
			cfg.Proxy.Port = *r.Proxy.Port
		}
		if r.Proxy.DomainSuffix != "" {
			cfg.Proxy.DomainSuffix = strings.ToLower(r.Proxy.DomainSuffix)
		}
		cfg.Proxy.TLS = r.Proxy.TLS
		if cfg.Proxy.TLS.Enabled && cfg.Proxy.TLS.Port == 0 {
			cfg.Proxy.TLS.Port = cfg.Proxy.EffectiveTLSPort()
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks value ranges, the project list and the groups.
func (c *Config) Validate() error {
	if err := c.validateProjects(); err != nil {
		return err
	}
	for name, port := range map[string]int{"web.port": c.Web.Port, "proxy.port": c.Proxy.Port, "proxy.tls.port": c.Proxy.TLS.Port} {
		if port < 0 || port > 65535 {
			return fmt.Errorf("globalconfig: %s must be between 0 and 65535, got %d", name, port)
		}
	}
	return nil
}

// Save writes the web and proxy sections of cfg to path atomically. Other
// content (comments, projects, groups, unknown keys) is left as it is; use
// AddProject, RemoveProject and MoveProject for the project list.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return edit(path, func(root *yaml.Node) error {
		for _, section := range []struct {
			key string
			v   any
		}{{"web", cfg.Web}, {"proxy", cfg.Proxy}} {
			var n yaml.Node
			if err := n.Encode(section.v); err != nil {
				return fmt.Errorf("globalconfig: marshal: %w", err)
			}
			if cur := mapValue(root, section.key); cur != nil {
				mergeNode(cur, &n)
				continue
			}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section.key}, &n)
		}
		return nil
	})
}

// mergeNode makes dst equal to src, keeping dst's comments: mappings merge
// key by key (keys missing from src are dropped), anything else is replaced.
func mergeNode(dst, src *yaml.Node) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		head, line, foot := dst.HeadComment, dst.LineComment, dst.FootComment
		*dst = *src
		dst.HeadComment, dst.LineComment, dst.FootComment = head, line, foot
		return
	}
	var content []*yaml.Node
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, val := src.Content[i], src.Content[i+1]
		if cur := mapValue(dst, key.Value); cur != nil {
			mergeNode(cur, val)
			for j := 0; j+1 < len(dst.Content); j += 2 {
				if dst.Content[j].Value == key.Value {
					content = append(content, dst.Content[j], cur)
					break
				}
			}
			continue
		}
		content = append(content, key, val)
	}
	dst.Content = content
}

// String summarizes the config for logs.
func (c *Config) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "web=%s:%d proxy=%s:%d suffix=%s", c.Web.Host, c.Web.Port, c.Proxy.Host, c.Proxy.Port, c.Proxy.DomainSuffix)
	if c.Proxy.TLS.Enabled {
		fmt.Fprintf(&sb, " tls=%d", c.Proxy.EffectiveTLSPort())
		if c.Proxy.TLS.HTTPRedirect {
			sb.WriteString(" (redirect)")
		}
	}
	return sb.String()
}
