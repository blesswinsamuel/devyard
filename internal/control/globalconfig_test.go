package control_test

import (
	"testing"

	"github.com/blesswinsamuel/devyard/internal/control"
	"github.com/blesswinsamuel/devyard/internal/protocol"
)

func validGlobalConfig() *protocol.GlobalConfig {
	return &protocol.GlobalConfig{
		Web:   &protocol.GlobalWebConfig{Host: "127.0.0.1", Port: 9090},
		Proxy: &protocol.GlobalProxyConfig{Host: "127.0.0.1", Port: 8080, DomainSuffix: "localhost"},
	}
}

func TestValidateGlobalConfig(t *testing.T) {
	if err := control.ValidateGlobalConfig(validGlobalConfig()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateGlobalConfigRejects(t *testing.T) {
	cases := map[string]*protocol.GlobalConfig{
		"nil":              nil,
		"missing web":      {Proxy: validGlobalConfig().Proxy},
		"missing proxy":    {Web: validGlobalConfig().Web},
		"empty web host":   {Web: &protocol.GlobalWebConfig{Host: "", Port: 9090}, Proxy: validGlobalConfig().Proxy},
		"bad web port":     {Web: &protocol.GlobalWebConfig{Host: "h", Port: 0}, Proxy: validGlobalConfig().Proxy},
		"empty proxy host": {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "", Port: 8080, DomainSuffix: "localhost"}},
		"bad proxy port":   {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "h", Port: 70000, DomainSuffix: "localhost"}},
		"empty suffix":     {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "h", Port: 8080, DomainSuffix: ""}},
		"bad tls port":     {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "h", Port: 8080, DomainSuffix: "localhost", Tls: &protocol.GlobalProxyTLSConfig{Enabled: true, Port: 70000}}},
		"cert without key": {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "h", Port: 8080, DomainSuffix: "localhost", Tls: &protocol.GlobalProxyTLSConfig{Enabled: true, CertFile: "c.pem"}}},
		"key without cert": {Web: validGlobalConfig().Web, Proxy: &protocol.GlobalProxyConfig{Host: "h", Port: 8080, DomainSuffix: "localhost", Tls: &protocol.GlobalProxyTLSConfig{Enabled: true, KeyFile: "k.pem"}}},
	}
	for name, cfg := range cases {
		if err := control.ValidateGlobalConfig(cfg); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestGlobalConfigProtoRoundTrip(t *testing.T) {
	orig := &protocol.GlobalConfig{
		Web: &protocol.GlobalWebConfig{Host: "0.0.0.0", Port: 8081},
		Proxy: &protocol.GlobalProxyConfig{
			Host:         "0.0.0.0",
			Port:         9091,
			DomainSuffix: "nip.io",
			Tls: &protocol.GlobalProxyTLSConfig{
				Enabled:      true,
				Port:         9443,
				CertFile:     "c.pem",
				KeyFile:      "k.pem",
				HttpRedirect: true,
			},
		},
	}
	got := control.GlobalConfigToProto(control.ProtoToGlobalConfig(orig))
	if got.Web.Host != orig.Web.Host || got.Web.Port != orig.Web.Port {
		t.Errorf("web round-trip mismatch: got %+v want %+v", got.Web, orig.Web)
	}
	if got.Proxy.Host != orig.Proxy.Host || got.Proxy.Port != orig.Proxy.Port || got.Proxy.DomainSuffix != orig.Proxy.DomainSuffix {
		t.Errorf("proxy round-trip mismatch: got %+v want %+v", got.Proxy, orig.Proxy)
	}
	if got.Proxy.Tls == nil ||
		got.Proxy.Tls.Enabled != orig.Proxy.Tls.Enabled ||
		got.Proxy.Tls.Port != orig.Proxy.Tls.Port ||
		got.Proxy.Tls.CertFile != orig.Proxy.Tls.CertFile ||
		got.Proxy.Tls.KeyFile != orig.Proxy.Tls.KeyFile ||
		got.Proxy.Tls.HttpRedirect != orig.Proxy.Tls.HttpRedirect {
		t.Errorf("proxy tls round-trip mismatch: got %+v want %+v", got.Proxy.Tls, orig.Proxy.Tls)
	}
}
