package integration_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort reserves an ephemeral port and releases it.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// proxyTestConfig exposes two services: web (scalar port, project default)
// and api (named ports). The metrics port has no listener, so its host
// exercises the 502 path while the service itself is running.
func proxyTestConfig() string {
	return `version: "1"
name: lc-test
proxy:
  default_service: web
services:
  web:
    command: python3 -m http.server 3000 --bind 127.0.0.1
    port: 3000
    restart: always
  api:
    command: python3 -m http.server 3001 --bind 127.0.0.1
    ports:
      http: 3001
      metrics: 3002
    restart: always
`
}

// writeGlobalProxyConfig pins the daemon's proxy to a free test port inside
// the test's isolated XDG_CONFIG_HOME.
func writeGlobalProxyConfig(t *testing.T, configDir string, port int) {
	t.Helper()
	dir := filepath.Join(configDir, "local-compose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	content := fmt.Sprintf("proxy:\n  host: 127.0.0.1\n  port: %d\n", port)
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
}

// proxyGet issues a GET with the given Host header against the proxy.
// Returns status code and body; ok=false when the request could not be made.
func proxyGet(t *testing.T, baseURL, host string) (int, string, bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", false
	}
	return resp.StatusCode, string(data), true
}

func TestE2E_Proxy(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	proxyPort := freePort(t)
	e := newEnv(t, proxyTestConfig())
	writeGlobalProxyConfig(t, e.config, proxyPort)

	if _, _, code := e.run(t, context.Background(), "up", "-d"); code != 0 {
		t.Fatalf("up -d failed")
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(proxyPort)

	// Routing: service host, project default, named port, unknown host.
	type proxyCase struct {
		host     string
		wantCode int
	}
	proxyCases := []proxyCase{
		{"web.lc-test.localhost", http.StatusOK},
		{"lc-test.localhost", http.StatusOK}, // project default service
		{"http.api.lc-test.localhost", http.StatusOK},
		{"metrics.api.lc-test.localhost", http.StatusBadGateway}, // running, but nothing listens on 3002
		{"unknown.lc-test.localhost", http.StatusNotFound},
	}
	for _, tc := range proxyCases {
		waitForCond(t, 10*time.Second, func() bool {
			status, _, ok := proxyGet(t, baseURL, tc.host)
			return ok && status == tc.wantCode
		}, fmt.Sprintf("host %s → %d", tc.host, tc.wantCode))
	}

	// Stopped service → 503 with a hint naming the service.
	if out, _, code := e.run(t, context.Background(), "stop", "web"); code != 0 {
		t.Fatalf("stop web failed: %s", out)
	}
	waitForCond(t, 10*time.Second, func() bool {
		status, body, ok := proxyGet(t, baseURL, "web.lc-test.localhost")
		return ok && status == http.StatusServiceUnavailable && strings.Contains(body, "not running")
	}, "503 page for stopped service")

	// ps shows the URL column for exposed services.
	psOut, _, code := e.run(t, context.Background(), "ps")
	if code != 0 {
		t.Fatalf("ps failed: %s", psOut)
	}
	if !strings.Contains(psOut, "web.lc-test.localhost:"+strconv.Itoa(proxyPort)) {
		t.Errorf("ps output missing proxy URL: %s", psOut)
	}
}
