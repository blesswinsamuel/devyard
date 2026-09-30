// Package proxy_test covers the reverse proxy: Host-based routing to
// services, the default service, named ports, and error pages.
package proxy_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

// web listens on its port; api's "metrics" port has no listener (502).
const cfg = `version: "1"
proxy:
  default_service: web
services:
  web:
    command: {{fixture "httpecho"}}
    port: {{port "web"}}
    env:
      PORT: "{{port "web"}}"
      NAME: web
  api:
    command: {{fixture "httpecho"}}
    ports:
      http: {{port "api"}}
      metrics: {{port "metrics"}}
    env:
      PORT: "{{port "api"}}"
      NAME: api
  hidden:
    command: {{fixture "ticker"}} -interval 1s
`

func setup(t *testing.T, name string) (*harness.Project, *harness.Daemon, *harness.Watcher) {
	t.Helper()
	sb := harness.New(t)
	p := sb.WriteProject(name, cfg, nil)
	p.Start()
	d := sb.Daemon()
	w := d.Watch(context.Background())
	p.WaitRunning(w)
	return p, d, w
}

func TestProxy_RoutesByHost(t *testing.T) {
	t.Parallel()
	p, d, _ := setup(t, "px")
	cases := []struct {
		host string
		code int
		body string
	}{
		{"web.px.localhost", http.StatusOK, "name=web"},
		{"px.localhost", http.StatusOK, "name=web"}, // default_service
		{"http.api.px.localhost", http.StatusOK, "name=api"},
		{"api.px.localhost", http.StatusOK, "name=api"}, // first port is the default
		{"metrics.api.px.localhost", http.StatusBadGateway, ""},
		{"unknown.px.localhost", http.StatusNotFound, ""},
		{"hidden.px.localhost", http.StatusNotFound, ""}, // no port => not routed
	}
	for _, tc := range cases {
		harness.Eventually(t, fmt.Sprintf("%s -> %d", tc.host, tc.code), func(c *harness.C) {
			r := d.ProxyGet(tc.host, "/some/path")
			if r.Err != nil || r.Code != tc.code || !strings.Contains(r.Body, tc.body) {
				c.Errorf("got %d %q err=%v", r.Code, clip(r.Body), r.Err)
			}
		})
	}
	// The original Host header is preserved.
	r := d.ProxyGet("web.px.localhost:1234", "/x")
	if !strings.Contains(r.Body, "host=web.px.localhost:1234") {
		t.Errorf("Host not preserved: %q", r.Body)
	}
	_ = p
}

func TestProxy_StoppedService503(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "px503")
	_, err := d.Client().StopService(d.Ctx(), connect.NewRequest(&v1.StopServiceRequest{Project: "px503", Service: "web"}))
	harness.NoError(t, err, "StopService")
	w.WaitFor(t, "web stopped", func(s harness.State) bool { return s.ServiceIs("px503", "web", "stopped") })
	harness.Eventually(t, "503 page", func(c *harness.C) {
		r := d.ProxyGet("web.px503.localhost", "/")
		if r.Code != http.StatusServiceUnavailable || !strings.Contains(r.Body, "web") {
			c.Errorf("got %d %q", r.Code, clip(r.Body))
		}
	})
	_, err = d.Client().StartService(d.Ctx(), connect.NewRequest(&v1.StartServiceRequest{Project: "px503", Service: "web"}))
	harness.NoError(t, err, "StartService")
	harness.Eventually(t, "routes again", func(c *harness.C) {
		if r := d.ProxyGet("web.px503.localhost", "/"); r.Code != http.StatusOK {
			c.Errorf("got %d", r.Code)
		}
	})
}

func TestProxy_URLsOnServices(t *testing.T) {
	t.Parallel()
	_, d, w := setup(t, "pxurl")
	proxy := strings.TrimPrefix(d.ProxyURL(), "http://")
	port := proxy[strings.LastIndexByte(proxy, ':')+1:]
	st := w.State()
	want := "http://web.pxurl.localhost:" + port
	found := false
	for _, u := range st.Service("pxurl", "web").GetUrls() {
		if strings.HasPrefix(u, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("web urls %v missing %s", st.Service("pxurl", "web").GetUrls(), want)
	}
	if urls := st.Service("pxurl", "hidden").GetUrls(); len(urls) != 0 {
		t.Errorf("portless service has urls %v", urls)
	}
	if st.Project("pxurl").GetDefaultService() != "web" {
		t.Errorf("default_service = %q", st.Project("pxurl").GetDefaultService())
	}
}

// O10: after a reload that changes a routed service, the proxy serves the
// new process, never the old one again.
func TestLedger_O10_ProxyFollowsReload(t *testing.T) {
	t.Parallel()
	p, d, w := setup(t, "pxrl")
	harness.Eventually(t, "old version served", func(c *harness.C) {
		if r := d.ProxyGet("web.pxrl.localhost", "/"); !strings.Contains(r.Body, "name=web") {
			c.Errorf("%d %q", r.Code, clip(r.Body))
		}
	})
	old := w.State().ServicePid("pxrl", "web")
	p.WriteConfig(strings.Replace(cfg, "NAME: web", "NAME: web-v2", 1))
	_, err := d.Client().ReloadProject(d.Ctx(), connect.NewRequest(&v1.ReloadProjectRequest{Project: "pxrl"}))
	harness.NoError(t, err, "ReloadProject")
	w.WaitFor(t, "web restarted", func(s harness.State) bool {
		return s.Running("pxrl", "web") && s.ServicePid("pxrl", "web") != old
	})
	harness.Eventually(t, "new version served", func(c *harness.C) {
		if r := d.ProxyGet("web.pxrl.localhost", "/"); !strings.Contains(r.Body, "name=web-v2") {
			c.Errorf("%d %q", r.Code, clip(r.Body))
		}
	})
	harness.Consistently(t, "old version never comes back", time.Second, func(c *harness.C) {
		if r := d.ProxyGet("web.pxrl.localhost", "/"); r.Code == http.StatusOK && !strings.Contains(r.Body, "name=web-v2") {
			c.Errorf("served %q", clip(r.Body))
		}
	})
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
