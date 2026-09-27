package globalconfig_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blesswinsamuel/devyard/internal/globalconfig"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Host != globalconfig.DefaultHost {
		t.Errorf("Host = %q, want %q", cfg.Web.Host, globalconfig.DefaultHost)
	}
	if cfg.Web.Port != globalconfig.DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Web.Port, globalconfig.DefaultPort)
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warnings: %s", buf.String())
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `web:
  host: 0.0.0.0
  port: 8080
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", cfg.Web.Host)
	}
	if cfg.Web.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Web.Port)
	}
}

func TestLoadAppliesDefaultsForEmptyFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `web: {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Host != globalconfig.DefaultHost {
		t.Errorf("Host = %q, want %q", cfg.Web.Host, globalconfig.DefaultHost)
	}
	if cfg.Web.Port != globalconfig.DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Web.Port, globalconfig.DefaultPort)
	}
}

func TestLoadIgnoresLegacyEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	// Legacy field is ignored by the schema (no error).
	content := `web:
  enabled: true
  host: 0.0.0.0
  port: 8080
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", cfg.Web.Host)
	}
	if cfg.Web.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Web.Port)
	}
}

func TestLoadUnknownFieldsWarn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `web:
  host: 127.0.0.1
unknown_field: hello
another_unknown: 42
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", cfg.Web.Host)
	}
	warnings := buf.String()
	if !strings.Contains(warnings, "unknown_field") {
		t.Errorf("warnings missing 'unknown_field': %s", warnings)
	}
	if !strings.Contains(warnings, "another_unknown") {
		t.Errorf("warnings missing 'another_unknown': %s", warnings)
	}
}

func TestConfigPathUsesXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	path, err := globalconfig.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if path != "/custom/config/devyard/config.yml" {
		t.Errorf("path = %q, want /custom/config/devyard/config.yml", path)
	}
}

func TestConfigPathDefaultsToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome")
	path, err := globalconfig.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want := "/tmp/fakehome/.config/devyard/config.yml"
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg := globalconfig.Defaults()
	cfg.Web.Host = "0.0.0.0"
	cfg.Web.Port = 12345

	if err := globalconfig.SaveForTest(path, &cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var buf bytes.Buffer
	loaded, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Web.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", loaded.Web.Host)
	}
	if loaded.Web.Port != 12345 {
		t.Errorf("Port = %d, want 12345", loaded.Web.Port)
	}
}

func TestLoadProxyDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Proxy.Host != globalconfig.DefaultProxyHost {
		t.Errorf("Proxy.Host = %q, want %q", cfg.Proxy.Host, globalconfig.DefaultProxyHost)
	}
	if cfg.Proxy.Port != globalconfig.DefaultProxyPort {
		t.Errorf("Proxy.Port = %d, want %d", cfg.Proxy.Port, globalconfig.DefaultProxyPort)
	}
	if cfg.Proxy.DomainSuffix != globalconfig.DefaultProxyDomainSuffix {
		t.Errorf("Proxy.DomainSuffix = %q, want %q", cfg.Proxy.DomainSuffix, globalconfig.DefaultProxyDomainSuffix)
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warnings: %s", buf.String())
	}
}

func TestLoadProxyOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `proxy:
  host: 0.0.0.0
  port: 9091
  domain_suffix: 192-168-1-5.nip.io
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Proxy.Host != "0.0.0.0" {
		t.Errorf("Proxy.Host = %q, want 0.0.0.0", cfg.Proxy.Host)
	}
	if cfg.Proxy.Port != 9091 {
		t.Errorf("Proxy.Port = %d, want 9091", cfg.Proxy.Port)
	}
	if cfg.Proxy.DomainSuffix != "192-168-1-5.nip.io" {
		t.Errorf("Proxy.DomainSuffix = %q, want 192-168-1-5.nip.io", cfg.Proxy.DomainSuffix)
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warnings: %s", buf.String())
	}
}

func TestSaveAndLoadProxyRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg := globalconfig.Defaults()
	cfg.Proxy.Host = "0.0.0.0"
	cfg.Proxy.Port = 8081
	cfg.Proxy.DomainSuffix = "dev.lan"

	if err := globalconfig.SaveForTest(path, &cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var buf bytes.Buffer
	loaded, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Proxy.Host != "0.0.0.0" || loaded.Proxy.Port != 8081 || loaded.Proxy.DomainSuffix != "dev.lan" {
		t.Errorf("proxy roundtrip: %+v", loaded.Proxy)
	}
}

func TestLoadProxyTLS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `proxy:
  host: 0.0.0.0
  port: 8080
  domain_suffix: dev.lan
  tls:
    enabled: true
    port: 9443
    cert_file: /path/to/cert.pem
    key_file: /path/to/key.pem
    http_redirect: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Proxy.TLS.Enabled {
		t.Errorf("Proxy.TLS.Enabled = false, want true")
	}
	if cfg.Proxy.TLS.Port != 9443 {
		t.Errorf("Proxy.TLS.Port = %d, want 9443", cfg.Proxy.TLS.Port)
	}
	if cfg.Proxy.TLS.CertFile != "/path/to/cert.pem" {
		t.Errorf("Proxy.TLS.CertFile = %q, want /path/to/cert.pem", cfg.Proxy.TLS.CertFile)
	}
	if cfg.Proxy.TLS.KeyFile != "/path/to/key.pem" {
		t.Errorf("Proxy.TLS.KeyFile = %q, want /path/to/key.pem", cfg.Proxy.TLS.KeyFile)
	}
	if !cfg.Proxy.TLS.HTTPRedirect {
		t.Errorf("Proxy.TLS.HTTPRedirect = false, want true")
	}
	if !strings.Contains(cfg.String(), "tls=9443 (redirect)") {
		t.Errorf("cfg.String() missing tls: %s", cfg.String())
	}
}

func TestLoadProxyTLSDefaultsEffectivePort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `proxy:
  port: 80
  tls:
    enabled: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	cfg, err := globalconfig.LoadForTest(path, &buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Proxy.TLS.Port != 443 {
		t.Errorf("Proxy.TLS.Port = %d, want 443", cfg.Proxy.TLS.Port)
	}
}
