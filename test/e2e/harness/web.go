package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/blesswinsamuel/devyard/internal/gen/proto/devyard/v1/devyardv1connect"
)

var urlRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

// WebURLFromCLI runs `devyard web` and returns the first URL it prints (it
// may carry the per-install token as a query parameter).
func (d *Daemon) WebURLFromCLI() string {
	d.sb.t.Helper()
	r := d.sb.CLI("web").MustSucceed(d.sb.t)
	m := urlRE.FindString(r.Stdout)
	if m == "" {
		m = urlRE.FindString(r.Stderr)
	}
	if m == "" {
		d.sb.t.Fatalf("`devyard web` printed no URL:\n%s", r)
	}
	return m
}

// WebClient returns an HTTP client whose cookie jar has been primed by
// visiting the URL printed by `devyard web`, so it carries whatever auth
// cookie the dashboard sets.
func (d *Daemon) WebClient() *http.Client {
	d.sb.t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: Scale(30 * time.Second)}
	u := d.WebURLFromCLI()
	resp, err := hc.Get(u)
	if err != nil {
		d.sb.t.Fatalf("GET %s: %v", u, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		d.sb.t.Fatalf("GET %s (dashboard URL from `devyard web`): status %d", u, resp.StatusCode)
	}
	return hc
}

// WebHost returns the web listener's host:port.
func (d *Daemon) WebHost() string {
	d.sb.t.Helper()
	return strings.TrimPrefix(d.WebURL(), "http://")
}

// WebRPCClient returns a Connect client talking to the web listener (as the
// browser does), authenticated like WebClient.
func (d *Daemon) WebRPCClient(opts ...connect.ClientOption) devyardv1connect.DaemonServiceClient {
	d.sb.t.Helper()
	return devyardv1connect.NewDaemonServiceClient(d.WebClient(), d.WebURL(), opts...)
}

// HTTPResult is a plain HTTP response.
type HTTPResult struct {
	Code   int
	Body   string
	Header http.Header
	Err    error
}

// HTTPDo sends a request with an optional Host override.
func HTTPDo(client *http.Client, method, rawURL, host string, header http.Header, body io.Reader) HTTPResult {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return HTTPResult{Err: err}
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResult{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return HTTPResult{Code: resp.StatusCode, Body: string(data), Header: resp.Header, Err: err}
}

// ProxyGet GETs path on the proxy listener with the given Host header.
func (d *Daemon) ProxyGet(host, path string) HTTPResult {
	d.sb.t.Helper()
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return HTTPDo(client, http.MethodGet, d.ProxyURL()+path, host, nil, nil)
}

// sameOrigin returns the Origin a browser would send for the dashboard.
func (d *Daemon) sameOrigin() string {
	u, _ := url.Parse(d.WebURL())
	return u.Scheme + "://" + u.Host
}

func ctxWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, Scale(d))
}
