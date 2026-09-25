package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
	srv := NewServer("127.0.0.1:0", "localhost", resolver)
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
