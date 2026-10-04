// Package web serves the dashboard: the embedded SPA, the DaemonService
// ConnectRPC API and the /ws/attach websocket for interactive sessions.
//
// Every request must carry an allowed Host header (loopback names, IP
// literals, "devyard", names under the proxy domain suffix, or configured
// extra hosts). This blocks DNS-rebinding attacks, where a malicious page
// resolves its own domain to 127.0.0.1 to reach the dashboard. Websockets
// additionally require a same-origin Origin header. When web.password_hash
// is set (`devyard auth set-password`), the API and websocket additionally
// require a login cookie (auth.go).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/runner"
	"github.com/blesswinsamuel/devyard/internal/sessions"
)

//go:embed all:dist
var distFS embed.FS

// HostPolicy decides which Host headers are accepted.
type HostPolicy struct {
	// DomainSuffix is the proxy domain suffix (names under it are allowed).
	DomainSuffix string
	// Extra are additional allowed hosts; "*.example.com" matches
	// subdomains.
	Extra []string
}

// Allowed reports whether host (with or without port) is acceptable.
func (p HostPolicy) Allowed(hostport string) bool {
	host := strings.ToLower(hostport)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		// IP literals cannot be rebound by DNS.
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "devyard" {
		return true
	}
	if s := strings.ToLower(p.DomainSuffix); s != "" && (host == s || strings.HasSuffix(host, "."+s)) {
		return true
	}
	for _, e := range p.Extra {
		e = strings.ToLower(e)
		if strings.HasPrefix(e, "*.") {
			if strings.HasSuffix(host, e[1:]) {
				return true
			}
		} else if host == e {
			return true
		}
	}
	return false
}

// Options configures the handler.
type Options struct {
	API      http.Handler // mounted at /devyard.v1.DaemonService/
	Sessions *sessions.Manager
	Hosts    func() HostPolicy
	// Auth, when non-nil, gates the API and /ws/attach behind a password.
	Auth *Authenticator
	Log  *slog.Logger
}

// apiPath is where the ConnectRPC API is mounted.
const apiPath = "/devyard.v1.DaemonService/"

// Handler returns the dashboard handler.
func Handler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(apiPath, opts.API)
	mux.HandleFunc("/ws/attach", func(w http.ResponseWriter, r *http.Request) { serveAttach(w, r, opts) })
	mux.Handle("/", spaHandler())
	var next http.Handler = mux
	if opts.Auth != nil {
		mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) { opts.Auth.serveLogin(w, r) })
		next = opts.Auth.wrap(mux)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !opts.Hosts().Allowed(r.Host) {
			http.Error(w, "devyard: host not allowed (add it to web.allowed_hosts in the global config)", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "." {
			if f, err := sub.Open(p); err == nil {
				_ = f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// Client-side routes fall back to index.html.
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "devyard: web UI not built (run `bun run build` in web/)", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// --- /ws/attach ---------------------------------------------------------------

type wsControl struct {
	Type   string `json:"type"`
	Target *struct {
		Kind      string `json:"kind"`
		Project   string `json:"project"`
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
	} `json:"target,omitempty"`
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

type wsEvent struct {
	Type      string `json:"type"`
	Code      string `json:"code,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	TTY       *bool  `json:"tty,omitempty"`
	Stdin     *bool  `json:"stdin,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// errorCode classifies a session error for the browser: not_found,
// not_running, invalid or error.
func errorCode(err error) string {
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound), errors.Is(err, engine.ErrNotFound):
		return "not_found"
	case errors.Is(err, engine.ErrNotRunning):
		return "not_running"
	case errors.Is(err, engine.ErrInvalid):
		return "invalid"
	}
	return "error"
}

func serveAttach(w http.ResponseWriter, r *http.Request, opts Options) {
	// Default AcceptOptions enforce Origin == Host (same origin).
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)
	ctx := r.Context()

	send := func(ev wsEvent) error {
		data, _ := json.Marshal(ev)
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, data)
	}

	typ, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var open wsControl
	if typ != websocket.MessageText || json.Unmarshal(data, &open) != nil || open.Type != "open" || open.Target == nil {
		_ = send(wsEvent{Type: "error", Message: "first message must be an open request"})
		return
	}
	t := open.Target
	sess, err := opts.Sessions.Open(ctx, sessions.Target{Kind: t.Kind, Project: t.Project, Name: t.Name, SessionID: t.SessionID}, open.Cols, open.Rows)
	if err != nil {
		_ = send(wsEvent{Type: "error", Code: errorCode(err), Message: err.Error()})
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	defer func() { _ = sess.Detach() }()
	tty, stdin := sess.TTY, sess.Stdin
	if err := send(wsEvent{Type: "ready", SessionID: sess.ID, TTY: &tty, Stdin: &stdin}); err != nil {
		return
	}

	// Browser → session.
	go func() {
		defer func() { _ = sess.Detach() }()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				_ = sess.Write(data)
				continue
			}
			var c wsControl
			if json.Unmarshal(data, &c) != nil {
				continue
			}
			switch c.Type {
			case "resize":
				_ = sess.Resize(c.Cols, c.Rows)
			case "eof":
				_ = sess.CloseStdin()
			case "close":
				_ = sess.Kill(context.WithoutCancel(ctx))
			}
		}
	}()

	// Session → browser. Output for this one session is written in order;
	// a slow browser only slows its own session (the runner drops output
	// for lagging attachments rather than blocking the process).
	for {
		out, err := sess.Read()
		if err != nil {
			var exit *runner.ExitError
			if errors.As(err, &exit) {
				code := exit.Status.ExitCode
				_ = send(wsEvent{Type: "exit", ExitCode: &code})
				_ = conn.Close(websocket.StatusNormalClosure, "")
			}
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = conn.Write(wctx, websocket.MessageBinary, out)
		cancel()
		if err != nil {
			return
		}
	}
}
