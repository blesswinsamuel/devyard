package globalconfig_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blesswinsamuel/local-compose/internal/globalconfig"
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
	if path != "/custom/config/local-compose/config.yml" {
		t.Errorf("path = %q, want /custom/config/local-compose/config.yml", path)
	}
}

func TestConfigPathDefaultsToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome")
	path, err := globalconfig.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want := "/tmp/fakehome/.config/local-compose/config.yml"
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
