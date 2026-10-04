package globalconfig_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
)

func parse(t *testing.T, content string) (*globalconfig.Config, string) {
	t.Helper()
	var warn bytes.Buffer
	cfg, err := globalconfig.Parse([]byte(content), &warn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg, warn.String()
}

func TestMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := globalconfig.Load(filepath.Join(t.TempDir(), "config.yml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*cfg, globalconfig.Defaults()) {
		t.Fatalf("got %+v", cfg)
	}
}

func TestExplicitValues(t *testing.T) {
	cfg, _ := parse(t, "web:\n  host: 0.0.0.0\n  port: 8081\n  allowed_hosts: [box.tailnet.ts.net]\nproxy:\n  port: 8082\n  domain_suffix: Dev.LAN\n")
	if cfg.Web.Host != "0.0.0.0" || cfg.Web.Port != 8081 || cfg.Proxy.Port != 8082 || cfg.Proxy.DomainSuffix != "dev.lan" {
		t.Fatalf("got %+v", cfg)
	}
	if len(cfg.Web.AllowedHosts) != 1 || cfg.Web.AllowedHosts[0] != "box.tailnet.ts.net" {
		t.Fatalf("allowed hosts: %v", cfg.Web.AllowedHosts)
	}
	if cfg.Proxy.Host != globalconfig.DefaultProxyHost {
		t.Fatalf("proxy host default not applied: %q", cfg.Proxy.Host)
	}
}

func TestEmptySectionsGetDefaults(t *testing.T) {
	cfg, _ := parse(t, "web: {}\nproxy: {}\n")
	if !reflect.DeepEqual(*cfg, globalconfig.Defaults()) {
		t.Fatalf("got %+v", cfg)
	}
}

func TestPortZeroMeansEphemeral(t *testing.T) {
	cfg, _ := parse(t, "web:\n  port: 0\nproxy:\n  port: 0\n")
	if cfg.Web.Port != 0 || cfg.Proxy.Port != 0 {
		t.Fatalf("got %+v", cfg)
	}
}

func TestInvalidPort(t *testing.T) {
	if _, err := globalconfig.Parse([]byte("web:\n  port: 70000\n"), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestUnknownFieldsWarn(t *testing.T) {
	_, warn := parse(t, "web:\n  host: 127.0.0.1\nunknown_field: 1\n")
	if !strings.Contains(warn, "unknown_field") {
		t.Fatalf("warnings: %q", warn)
	}
}

func TestTLS(t *testing.T) {
	cfg, _ := parse(t, "proxy:\n  tls:\n    enabled: true\n    port: 9443\n    http_redirect: true\n")
	if !cfg.Proxy.TLS.Enabled || cfg.Proxy.TLS.Port != 9443 || !strings.Contains(cfg.String(), "tls=9443 (redirect)") {
		t.Fatalf("got %+v %s", cfg.Proxy.TLS, cfg)
	}
	cfg, _ = parse(t, "proxy:\n  port: 80\n  tls:\n    enabled: true\n")
	if cfg.Proxy.TLS.Port != 443 {
		t.Fatalf("effective tls port %d", cfg.Proxy.TLS.Port)
	}
}

func TestPasswordHash(t *testing.T) {
	hash := "$2a$10$Sqvqa5SIKdLY0cCcn.UhL.exGZzTLtOjfTWPzvH8E9PLYJEFDBh6K"
	cfg, _ := parse(t, "web:\n  password_hash: "+hash+"\n")
	if cfg.Web.PasswordHash != hash {
		t.Fatalf("password hash: %q", cfg.Web.PasswordHash)
	}
	if strings.Contains(cfg.String(), hash) {
		t.Fatalf("String() leaks the password hash: %s", cfg)
	}
	cfg, _ = parse(t, "web: {}\n")
	if cfg.Web.PasswordHash != "" {
		t.Fatalf("default password hash: %q", cfg.Web.PasswordHash)
	}
}

func TestSaveRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devyard", "config.yml")
	in := globalconfig.Defaults()
	in.Web.Port = 0
	in.Proxy.DomainSuffix = "dev.lan"
	in.Web.AllowedHosts = []string{"a.example"}
	in.Web.PasswordHash = "$2a$10$Sqvqa5SIKdLY0cCcn.UhL.exGZzTLtOjfTWPzvH8E9PLYJEFDBh6K"
	if err := globalconfig.Save(path, &in); err != nil {
		t.Fatal(err)
	}
	out, err := globalconfig.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Web.Port != 0 || out.Proxy.DomainSuffix != "dev.lan" || len(out.Web.AllowedHosts) != 1 || out.Web.PasswordHash != in.Web.PasswordHash {
		t.Fatalf("roundtrip: %+v", out)
	}
}
