package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

// fakeResolver serves a canned route list; it implements Resolver.
type fakeResolver struct {
	routes []Route
}

func (f *fakeResolver) ProxyRoutes() []Route { return f.routes }

// startUpstream starts a test HTTP server returning body and returns its
// port. seenHost, when non-nil, records the Host header of the last request.
func startUpstream(t *testing.T, body string, seenHost *string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenHost != nil {
			*seenHost = r.Host
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	var p int
	if _, err := fmt.Sscanf(srv.Listener.Addr().String(), "127.0.0.1:%d", &p); err != nil {
		t.Fatalf("parse upstream addr: %v", err)
	}
	return p
}

func newTestServer(t *testing.T, resolver Resolver) *Server {
	t.Helper()
	srv := NewServer(ServerOptions{
		Addr:         "127.0.0.1:0",
		DomainSuffix: "localhost",
		Resolver:     resolver,
	})
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func getWithHost(t *testing.T, url, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s (host %s): %v", url, host, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(data)
}

func TestProxyRouting(t *testing.T) {
	t.Parallel()

	seenHost := ""
	webPort := startUpstream(t, "hello from web", nil)
	apiPort := startUpstream(t, "hello from api", nil)
	homePort := startUpstream(t, "hello from home", &seenHost)

	resolver := &fakeResolver{routes: []Route{
		{Project: "proj", Service: "web", Port: webPort, Status: "running"},
		{Project: "proj", Service: "api", PortName: "metrics", Port: apiPort, Status: "running"},
		{Project: "proj", Service: "home", Label: "myhome", Port: homePort, Status: "running", IsDefault: true},
	}}
	srv := newTestServer(t, resolver)

	tests := []struct {
		host     string
		wantCode int
		wantBody string
	}{
		{"web.proj.localhost", http.StatusOK, "hello from web"},
		{"WEB.Proj.LOCALHOST", http.StatusOK, "hello from web"},
		{"api.proj.localhost", http.StatusOK, "hello from api"},
		{"metrics.api.proj.localhost", http.StatusOK, "hello from api"},
		{"myhome.proj.localhost", http.StatusOK, "hello from home"},
		{"proj.localhost", http.StatusOK, "hello from home"},
		{"unknown.proj.localhost", http.StatusNotFound, "Unknown service"},
	}
	for _, tc := range tests {
		resp, body := getWithHost(t, "http://"+srv.Addr()+"/path", tc.host)
		if resp.StatusCode != tc.wantCode {
			t.Errorf("host %s: status = %d, want %d", tc.host, resp.StatusCode, tc.wantCode)
		}
		if !strings.Contains(body, tc.wantBody) {
			t.Errorf("host %s: body %q does not contain %q", tc.host, body, tc.wantBody)
		}
	}

	// The original Host header must reach the upstream untouched.
	if _, _ = getWithHost(t, "http://"+srv.Addr()+"/", "myhome.proj.localhost"); seenHost != "myhome.proj.localhost" {
		t.Errorf("upstream Host = %q, want %q", seenHost, "web.proj.localhost")
	}
}

func TestProxyStoppedService(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{routes: []Route{
		{Project: "proj", Service: "web", Port: 3000, Status: "stopped"},
	}}
	srv := newTestServer(t, resolver)

	resp, body := getWithHost(t, "http://"+srv.Addr()+"/", "web.proj.localhost")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if !strings.Contains(body, "web") || !strings.Contains(body, "stopped") {
		t.Errorf("error page missing service name/status: %s", body)
	}
}

func TestProxyUpstreamUnreachable(t *testing.T) {
	t.Parallel()

	// Reserve a port, then close the listener so nothing serves on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	resolver := &fakeResolver{routes: []Route{
		{Project: "proj", Service: "dead", Port: port, Status: "running"},
	}}
	srv := newTestServer(t, resolver)

	resp, body := getWithHost(t, "http://"+srv.Addr()+"/", "dead.proj.localhost")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(body, "unreachable") {
		t.Errorf("error page missing 'unreachable': %s", body)
	}
}

func TestServiceHostsHelper(t *testing.T) {
	t.Parallel()

	svc := config.Service{
		Ports: &config.Ports{Entries: []config.ServicePort{
			{Name: "http", Port: 3000},
			{Name: "metrics", Port: 9100},
		}},
	}
	hosts := ServiceHosts("proj", "web", svc, true, "localhost")
	want := []string{
		"proj.localhost",
		"web.proj.localhost",
		"http.web.proj.localhost",
		"metrics.web.proj.localhost",
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("ServiceHosts = %v, want %v", hosts, want)
	}

	if got := ServiceHosts("proj", "db", config.Service{}, false, "localhost"); got != nil {
		t.Errorf("ServiceHosts with no ports = %v, want nil", got)
	}
}

func TestProxyTLSAndDualListening(t *testing.T) {
	t.Parallel()

	webPort := startUpstream(t, "hello from tls web", nil)
	resolver := &fakeResolver{routes: []Route{
		{Project: "proj", Service: "web", Port: webPort, Status: "running"},
	}}

	caDir := t.TempDir()
	cm, err := NewCertManager(CertManagerOptions{
		CADir: caDir,
	})
	if err != nil {
		t.Fatalf("NewCertManager: %v", err)
	}

	srv := NewServer(ServerOptions{
		Addr:         "127.0.0.1:0",
		DomainSuffix: "localhost",
		Resolver:     resolver,
		TLS: TLSOptions{
			Enabled:   true,
			Addr:      "127.0.0.1:0",
			TLSConfig: cm.TLSConfig(),
		},
	})
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Build TLS client trusting the test CA
	certPool := x509.NewCertPool()
	if cm.caCert != nil {
		certPool.AddCert(cm.caCert)
	}
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				ServerName: "web.proj.localhost",
			},
		},
	}

	// 1. Test HTTPS request
	req, err := http.NewRequest(http.MethodGet, "https://"+srv.TLSAddr()+"/test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "web.proj.localhost"
	resp, err := tlsClient.Do(req)
	if err != nil {
		t.Fatalf("GET https %s: %v", srv.TLSAddr(), err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTPS status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "hello from tls web" {
		t.Errorf("HTTPS body = %q, want 'hello from tls web'", string(body))
	}

	// 2. Test plain HTTP request to dual-listening HTTP port
	httpResp, httpBody := getWithHost(t, "http://"+srv.Addr()+"/test", "web.proj.localhost")
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", httpResp.StatusCode)
	}
	if httpBody != "hello from tls web" {
		t.Errorf("HTTP body = %q, want 'hello from tls web'", httpBody)
	}
}

func TestProxyHTTPRedirect(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{routes: []Route{
		{Project: "proj", Service: "web", Port: 8080, Status: "running"},
	}}

	caDir := t.TempDir()
	cm, err := NewCertManager(CertManagerOptions{
		CADir: caDir,
	})
	if err != nil {
		t.Fatalf("NewCertManager: %v", err)
	}

	srv := NewServer(ServerOptions{
		Addr:         "127.0.0.1:0",
		DomainSuffix: "localhost",
		Resolver:     resolver,
		TLS: TLSOptions{
			Enabled:      true,
			Addr:         "127.0.0.1:0",
			TLSConfig:    cm.TLSConfig(),
			HTTPRedirect: true,
		},
	})
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Send plain HTTP request without following redirects
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/my/path?foo=bar", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "web.proj.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET http: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want 307", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://web.proj.localhost:") || !strings.HasSuffix(loc, "/my/path?foo=bar") {
		t.Errorf("Location = %q, want https://web.proj.localhost:<port>/my/path?foo=bar", loc)
	}
}

func TestCertManagerCustomCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "custom.crt")
	keyPath := filepath.Join(dir, "custom.key")

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(12345),
		Subject: pkix.Name{
			CommonName: "custom.domain.com",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		DNSNames:              []string{"custom.domain.com"},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, _ := x509.MarshalECPrivateKey(privKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	cm, err := NewCertManager(CertManagerOptions{
		CertFile: certPath,
		KeyFile:  keyPath,
	})
	if err != nil {
		t.Fatalf("NewCertManager: %v", err)
	}
	cert, err := cm.GetCertificate(&tls.ClientHelloInfo{ServerName: "custom.domain.com"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("expected valid certificate, got nil/empty")
	}
}

func TestProxyDashboardRouting(t *testing.T) {
	t.Parallel()

	dashboardCalled := false
	dashboardHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dashboard response"))
	})

	srv := NewServer(ServerOptions{
		Addr:             "127.0.0.1:0",
		DomainSuffix:     "localhost",
		Resolver:         &fakeResolver{},
		DashboardHandler: dashboardHandler,
	})
	if err := srv.ListenAndServe(); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	defer func() { _ = srv.Close() }()

	testHosts := []string{
		"localhost",
		"127.0.0.1",
		"devyard.localhost",
		"devyard",
		"DEVYARD.localhost",
	}

	for _, host := range testHosts {
		dashboardCalled = false
		resp, body := getWithHost(t, "http://"+srv.Addr()+"/", host)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("host %s: status = %d, want 200", host, resp.StatusCode)
		}
		if !dashboardCalled {
			t.Errorf("host %s: DashboardHandler was not called", host)
		}
		if body != "dashboard response" {
			t.Errorf("host %s: body = %q, want 'dashboard response'", host, body)
		}
	}
}
