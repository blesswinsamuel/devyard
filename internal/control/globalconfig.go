package control

import (
	"fmt"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
	"github.com/blesswinsamuel/devyard/internal/protocol"
)

// GlobalConfigToProto converts a globalconfig.Config to its wire form.
func GlobalConfigToProto(cfg *globalconfig.Config) *protocol.GlobalConfig {
	return &protocol.GlobalConfig{
		Web: &protocol.GlobalWebConfig{
			Host: cfg.Web.Host,
			Port: int32(cfg.Web.Port),
		},
		Proxy: &protocol.GlobalProxyConfig{
			Host:         cfg.Proxy.Host,
			Port:         int32(cfg.Proxy.Port),
			DomainSuffix: cfg.Proxy.DomainSuffix,
			Tls: &protocol.GlobalProxyTLSConfig{
				Enabled:      cfg.Proxy.TLS.Enabled,
				Port:         int32(cfg.Proxy.TLS.Port),
				CertFile:     cfg.Proxy.TLS.CertFile,
				KeyFile:      cfg.Proxy.TLS.KeyFile,
				HttpRedirect: cfg.Proxy.TLS.HTTPRedirect,
			},
		},
	}
}

// ProtoToGlobalConfig converts a wire GlobalConfig to a globalconfig.Config.
func ProtoToGlobalConfig(cfg *protocol.GlobalConfig) *globalconfig.Config {
	if cfg == nil {
		d := globalconfig.Defaults()
		return &d
	}
	out := globalconfig.Defaults()
	if cfg.Web != nil {
		out.Web.Host = cfg.Web.Host
		out.Web.Port = int(cfg.Web.Port)
	}
	if cfg.Proxy != nil {
		out.Proxy.Host = cfg.Proxy.Host
		out.Proxy.Port = int(cfg.Proxy.Port)
		out.Proxy.DomainSuffix = cfg.Proxy.DomainSuffix
		if cfg.Proxy.Tls != nil {
			out.Proxy.TLS.Enabled = cfg.Proxy.Tls.Enabled
			out.Proxy.TLS.Port = int(cfg.Proxy.Tls.Port)
			out.Proxy.TLS.CertFile = cfg.Proxy.Tls.CertFile
			out.Proxy.TLS.KeyFile = cfg.Proxy.Tls.KeyFile
			out.Proxy.TLS.HTTPRedirect = cfg.Proxy.Tls.HttpRedirect
		}
	}
	return &out
}

// ValidateGlobalConfig checks user-supplied values before persisting them.
func ValidateGlobalConfig(cfg *protocol.GlobalConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if cfg.Web == nil || cfg.Proxy == nil {
		return fmt.Errorf("web and proxy sections are required")
	}
	if cfg.Web.Host == "" {
		return fmt.Errorf("web.host is required")
	}
	if cfg.Web.Port < 1 || cfg.Web.Port > 65535 {
		return fmt.Errorf("web.port must be between 1 and 65535")
	}
	if cfg.Proxy.Host == "" {
		return fmt.Errorf("proxy.host is required")
	}
	if cfg.Proxy.Port < 1 || cfg.Proxy.Port > 65535 {
		return fmt.Errorf("proxy.port must be between 1 and 65535")
	}
	if cfg.Proxy.DomainSuffix == "" {
		return fmt.Errorf("proxy.domain_suffix is required")
	}
	if cfg.Proxy.Tls != nil && cfg.Proxy.Tls.Enabled {
		if cfg.Proxy.Tls.Port < 0 || cfg.Proxy.Tls.Port > 65535 {
			return fmt.Errorf("proxy.tls.port must be between 1 and 65535")
		}
		if (cfg.Proxy.Tls.CertFile != "" && cfg.Proxy.Tls.KeyFile == "") ||
			(cfg.Proxy.Tls.CertFile == "" && cfg.Proxy.Tls.KeyFile != "") {
			return fmt.Errorf("proxy.tls.cert_file and proxy.tls.key_file must both be provided or both be empty")
		}
	}
	return nil
}
