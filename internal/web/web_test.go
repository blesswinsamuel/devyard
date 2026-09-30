package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostPolicy(t *testing.T) {
	p := HostPolicy{DomainSuffix: "dev.example.com", Extra: []string{"box.tail.ts.net", "*.corp.internal"}}
	allowed := []string{
		"localhost", "localhost:9090", "127.0.0.1:9090", "[::1]:9090", "192.168.1.5",
		"devyard", "devyard.dev.example.com", "frontend.devyard.dev.example.com", "dev.example.com",
		"web.app.localhost:8080", "box.tail.ts.net", "a.corp.internal",
	}
	for _, h := range allowed {
		if !p.Allowed(h) {
			t.Errorf("%q should be allowed", h)
		}
	}
	denied := []string{"", "evil.example", "evil.example:9090", "localhost.evil.example", "example.com", "box.tail.ts.net.evil.io", "corp.internal"}
	for _, h := range denied {
		if p.Allowed(h) {
			t.Errorf("%q should be denied", h)
		}
	}
}

func TestHandlerRejectsForeignHost(t *testing.T) {
	h := Handler(Options{
		API:   http.NotFoundHandler(),
		Hosts: func() HostPolicy { return HostPolicy{DomainSuffix: "localhost"} },
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/projects/x", nil)
	req.Host = "127.0.0.1:9090"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("loopback rejected")
	}
}
