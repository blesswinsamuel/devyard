// Package web_test covers the dashboard listener: the SPA, Connect RPC over
// HTTP, and the security gates (Host allowlist, Origin check, install
// token) that close the DNS-rebinding shell hole (O7).
package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1"
	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func TestMain(m *testing.M) { harness.Main(m) }

const rpcPath = "/devyard.v1.DaemonService/GetDaemon"

func jsonHeader() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return h
}

func TestWeb_IndexServed(t *testing.T) {
	t.Parallel()
	if !harness.WebDistBuilt() {
		t.Skip("internal/web/dist not built (cd web && bun run build)")
	}
	sb := harness.New(t)
	d := sb.Daemon()
	r := harness.HTTPDo(d.WebClient(), http.MethodGet, d.WebURL()+"/", "", nil, nil)
	if r.Code != http.StatusOK || !strings.Contains(strings.ToLower(r.Body), "<!doctype html") {
		t.Fatalf("GET /: %d %q", r.Code, clip(r.Body))
	}
	// Client-side routes fall back to index.html.
	r = harness.HTTPDo(d.WebClient(), http.MethodGet, d.WebURL()+"/projects/x/services/y", "", nil, nil)
	if r.Code != http.StatusOK || !strings.Contains(strings.ToLower(r.Body), "<!doctype html") {
		t.Errorf("SPA fallback: %d", r.Code)
	}
}

func TestWeb_ConnectJSONRPC(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	r := harness.HTTPDo(d.WebClient(), http.MethodPost, d.WebURL()+rpcPath, "", jsonHeader(), strings.NewReader("{}"))
	if r.Code != http.StatusOK {
		t.Fatalf("POST %s: %d %q", rpcPath, r.Code, clip(r.Body))
	}
	var resp struct {
		Info struct {
			Pid     int    `json:"pid"`
			WebAddr string `json:"webAddr"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(r.Body), &resp); err != nil {
		t.Fatalf("decode %q: %v", r.Body, err)
	}
	if resp.Info.Pid != d.Pid() || resp.Info.WebAddr != strings.TrimPrefix(d.WebURL(), "http://") {
		t.Errorf("GetDaemon over HTTP: %+v", resp.Info)
	}
}

func TestWeb_ConnectClientOverHTTP(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("webrpc", `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	c := d.WebRPCClient()
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(10*time.Second))
	defer cancel()
	resp, err := c.GetState(ctx, connect.NewRequest(&v1.GetStateRequest{}))
	harness.NoError(t, err, "GetState over the web listener")
	st := harness.StateFromSnapshot(resp.Msg.GetRevision(), resp.Msg.GetSnapshot())
	if st.Project("webrpc") == nil {
		t.Errorf("project missing from web GetState:\n%s", st)
	}
	// Server streaming works over HTTP/1.1 too (the browser's Watch).
	stream, err := c.Watch(ctx, connect.NewRequest(&v1.WatchRequest{}))
	harness.NoError(t, err, "Watch over the web listener")
	defer func() { _ = stream.Close() }()
	if !stream.Receive() || stream.Msg().GetSnapshot() == nil {
		t.Errorf("no snapshot over web Watch: %v", stream.Err())
	}
}

// O7: requests whose Host isn't loopback (or an allowed host) are refused:
// a DNS-rebinding page can't reach the API or the SPA.
func TestLedger_O7_ForeignHostRejected(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	hc := d.WebClient()
	for _, host := range []string{"evil.example", "evil.example:80", "127.0.0.1.nip.io", "localhost.evil.example"} {
		if r := harness.HTTPDo(hc, http.MethodGet, d.WebURL()+"/", host, nil, nil); r.Code != http.StatusForbidden {
			t.Errorf("GET / Host=%s: %d, want 403", host, r.Code)
		}
		if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, host, jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusForbidden {
			t.Errorf("RPC Host=%s: %d, want 403", host, r.Code)
		}
	}
	port := d.WebHost()[strings.LastIndexByte(d.WebHost(), ':'):]
	for _, host := range []string{"localhost" + port, "127.0.0.1" + port} {
		if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, host, jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusOK {
			t.Errorf("RPC Host=%s: %d, want 200", host, r.Code)
		}
	}
}

// O7: the websocket (shell access) rejects foreign Origins and Hosts.
func TestLedger_O7_WebSocketForeignOriginRejected(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("wso", `version: "1"
services:
  a:
    command: {{fixture "ticker"}} -interval 1s
`, nil)
	p.Start()
	d := sb.Daemon()
	ctx, cancel := context.WithTimeout(context.Background(), harness.Scale(10*time.Second))
	defer cancel()
	for _, o := range []harness.WSDialOpts{
		{Origin: "http://evil.example"},
		{Origin: "null"},
		{Host: "evil.example", Origin: "http://evil.example"},
	} {
		conn, resp, err := d.DialWS(ctx, o)
		if err == nil {
			_ = conn.CloseNow()
			t.Errorf("websocket accepted with origin=%q host=%q", o.Origin, o.Host)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			code := 0
			if resp != nil {
				code = resp.StatusCode
			}
			t.Errorf("origin=%q host=%q: status %d, want 403 (%v)", o.Origin, o.Host, code, err)
		}
	}
	// Same-origin works.
	s := d.OpenWS(harness.WSTarget{Kind: "terminal", Project: "wso"}, 80, 24)
	s.Send(t, "echo ok-$((1+2))\r")
	s.Expect(t, "ok-3")
}

func TestWeb_AllowedHostsConfig(t *testing.T) {
	t.Parallel()
	sb := harness.New(t, harness.WithGlobalConfig(`web:
  host: 127.0.0.1
  port: 0
  allowed_hosts: [devyard.test]
proxy:
  host: 127.0.0.1
  port: 0
`))
	d := sb.Daemon()
	hc := d.WebClient()
	port := d.WebHost()[strings.LastIndexByte(d.WebHost(), ':'):]
	if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "devyard.test"+port, jsonHeader(), strings.NewReader("{}")); r.Code == http.StatusForbidden {
		t.Errorf("allowed host rejected")
	}
	if r := harness.HTTPDo(hc, http.MethodPost, d.WebURL()+rpcPath, "other.test"+port, jsonHeader(), strings.NewReader("{}")); r.Code != http.StatusForbidden {
		t.Errorf("non-allowed host: %d, want 403", r.Code)
	}
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
