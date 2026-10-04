// Package globalconfig loads and saves the user-level config file at
// $XDG_CONFIG_HOME/devyard/config.yml (see paths.Dirs.GlobalConfig). It holds
// settings that apply across all projects: the web dashboard listener and
// the reverse proxy.
package globalconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	// dashboard open. Applied after a daemon restart.
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
	Web   WebConfig   `yaml:"web"`
	Proxy ProxyConfig `yaml:"proxy"`
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
	Web *struct {
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
		if key != "web" && key != "proxy" && warn != nil {
			_, _ = fmt.Fprintf(warn, "devyard: warning: unknown field %q in global config\n", key)
		}
	}
	var r raw
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("globalconfig: parse: %w", err)
	}
	cfg := Defaults()
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

// Validate checks value ranges.
func (c *Config) Validate() error {
	for name, port := range map[string]int{"web.port": c.Web.Port, "proxy.port": c.Proxy.Port, "proxy.tls.port": c.Proxy.TLS.Port} {
		if port < 0 || port > 65535 {
			return fmt.Errorf("globalconfig: %s must be between 0 and 65535, got %d", name, port)
		}
	}
	return nil
}

// Save writes cfg to path atomically.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("globalconfig: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("globalconfig: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("globalconfig: write: %w", err)
	}
	return os.Rename(tmp, path)
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
