package integration_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2E_WebPortCollisionFailsDaemonStartup(t *testing.T) {
	t.Parallel()

	// Bind a port in this process so the daemon cannot listen on it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	busyPort := ln.Addr().(*net.TCPAddr).Port

	e := newEnv(t, `version: "1"
name: test-collision
services:
  echo:
    command: echo collision
`)

	// Overwrite global config to set web.port to busyPort
	dir := filepath.Join(e.config, "devyard")
	cfgContent := fmt.Sprintf("web:\n  host: 127.0.0.1\n  port: %d\nproxy:\n  host: 127.0.0.1\n  port: %d\n", busyPort, freePort(t))
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	_, stderr, code := e.run(t, context.Background(), "start")
	if code == 0 {
		t.Fatalf("expected start to fail with port collision, got exit 0")
	}
	if !strings.Contains(stderr, "web server:") || !strings.Contains(stderr, "address already in use") {
		t.Fatalf("expected error output about web server address already in use, got:\n%s", stderr)
	}
}

func TestE2E_ProxyPortCollisionFailsDaemonStartup(t *testing.T) {
	t.Parallel()

	// Bind a port in this process so the daemon cannot listen on it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	busyPort := ln.Addr().(*net.TCPAddr).Port

	e := newEnv(t, `version: "1"
name: test-collision-proxy
services:
  echo:
    command: echo collision
`)

	// Overwrite global config to set proxy.port to busyPort
	dir := filepath.Join(e.config, "devyard")
	cfgContent := fmt.Sprintf("web:\n  host: 127.0.0.1\n  port: %d\nproxy:\n  host: 127.0.0.1\n  port: %d\n", freePort(t), busyPort)
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	_, stderr, code := e.run(t, context.Background(), "start")
	if code == 0 {
		t.Fatalf("expected start to fail with port collision, got exit 0")
	}
	if !strings.Contains(stderr, "reverse proxy:") || !strings.Contains(stderr, "address already in use") {
		t.Fatalf("expected error output about reverse proxy address already in use, got:\n%s", stderr)
	}
}
