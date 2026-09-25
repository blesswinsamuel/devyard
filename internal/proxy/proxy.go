// Package proxy implements the daemon's reverse proxy: it routes requests by
// Host header to the local port a supervised service listens on, so services
// are reachable at named URLs like http://<service>.<project>.localhost.
//
// Routes are resolved live on every request through the Resolver (the
// orchestrator), so no cache invalidation is needed when projects start,
// stop, or change config. A service is forwarded to only while its
// supervisor reports it running; anything else gets a styled error page
// naming the service and its status.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/blesswinsamuel/local-compose/internal/config"
)

// Route is a single host→upstream mapping with the service's live status,
// provided by the daemon on every request.
type Route struct {
	// Label is the service's host label: the custom proxy.host when set.
	Label string
	// IsDefault marks the project-level default service; its route is also
	// served at <project>.<domain_suffix>.
	IsDefault bool
	// Project and Service name the route (used in error pages).
	Project string
	Service string
	// PortName is the named port ("" for the service's default port).
	PortName string
	// Port is the upstream port the service listens on.
	Port int
	// Status is the service's live supervisor status ("running", "exited",
	// "stopped", ...). Empty means unknown/treated as stopped.
	Status string
}

// Resolver supplies the current set of routes. It is consulted on every
// request, so route state is always fresh without cache invalidation.
type Resolver interface {
	ProxyRoutes() []Route
}

// Server is the reverse-proxy HTTP server owned by the daemon process.
type Server struct {
	addr         string
	domainSuffix string
	resolver     Resolver

	mu       sync.Mutex
	listener net.Listener
}

// NewServer creates a proxy server bound to addr serving routes under
// domainSuffix (e.g. "localhost"). Use port 0 to let the OS pick a port and
// read the actual address back with Addr.
func NewServer(addr, domainSuffix string, resolver Resolver) *Server {
	return &Server{
		addr:         addr,
		domainSuffix: strings.ToLower(domainSuffix),
		resolver:     resolver,
	}
}

// ListenAndServe starts serving. It returns once the listener is ready.
// Call Close to stop accepting connections.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("proxy: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	slog.Info("proxy listening", "addr", s.Addr(), "domain_suffix", s.domainSuffix)
	go func() {
		httpSrv := &http.Server{Handler: s}
		_ = httpSrv.Serve(ln)
	}()
	return nil
}

// Addr returns the actual listener address (useful when port 0 is used).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

// Close stops the listener. It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	ln := s.listener
	s.listener = nil
	s.mu.Unlock()
	if ln == nil {
		return nil
	}
	return ln.Close()
}

// ServeHTTP routes one request by Host header.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		writeErrorPage(w, http.StatusNotFound, "Unknown service",
			"Request has no Host header.")
		return
	}

	route, ok := s.routeIndex()[host]
	if !ok {
		writeErrorPage(w, http.StatusNotFound, "Unknown service",
			fmt.Sprintf("No service is exposed at %q.", host))
		return
	}
	if route.Status != "running" {
		status := route.Status
		if status == "" {
			status = "stopped"
		}
		writeErrorPage(w, http.StatusServiceUnavailable,
			fmt.Sprintf("Service %s is %s", route.Service, status),
			fmt.Sprintf("The service behind %s is not running (status: %s). Start it with `local-compose start %s`.", host, status, route.Service))
		return
	}
	s.proxyFor(route).ServeHTTP(w, r)
}

// routeIndex builds the host→route map from the resolver's live routes.
// Routes are considered in resolver order; the first route for a host wins.
func (s *Server) routeIndex() map[string]Route {
	routes := s.resolver.ProxyRoutes()
	idx := make(map[string]Route, len(routes))
	for _, route := range routes {
		for _, host := range routeHosts(route, s.domainSuffix) {
			if _, exists := idx[host]; !exists {
				idx[host] = route
			}
		}
	}
	return idx
}

// routeHosts returns every hostname a route answers to: the project URL for
// the default service, the service label host, and one host per named port.
func routeHosts(r Route, domainSuffix string) []string {
	suffix := r.Project + "." + domainSuffix
	var hosts []string
	if r.IsDefault {
		hosts = append(hosts, suffix)
	}
	label := r.Label
	if label == "" {
		label = r.Service
	}
	hosts = append(hosts, label+"."+suffix)
	if r.PortName != "" {
		hosts = append(hosts, r.PortName+"."+label+"."+suffix)
	}
	return hosts
}

func (s *Server) proxyFor(route Route) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{
				Scheme: "http",
				Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(route.Port)),
			})
			// Preserve the original Host header so applications that route on
			// it (virtual hosts, URL generation) still see the named URL.
			pr.Out.Host = pr.In.Host
		},
		FlushInterval: -1, // flush immediately so streaming responses work
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			slog.Debug("proxy upstream error", "service", route.Service, "port", route.Port, "error", err)
			writeErrorPage(w, http.StatusBadGateway,
				fmt.Sprintf("Service %s is unreachable", route.Service),
				fmt.Sprintf("The proxy could not connect to 127.0.0.1:%d. Is the service listening on that port?", route.Port))
		},
	}
}

// writeErrorPage renders the small styled error page used for unroutable or
// unreachable services.
func writeErrorPage(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	body := fmt.Sprintf(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>%s — local-compose</title></head>
<body style="font-family: ui-sans-serif, system-ui, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #111; color: #eee;">
  <div style="text-align: center; padding: 2rem;">
    <div style="font-size: 48px; margin-bottom: 8px;">%d</div>
    <h1 style="font-size: 20px; font-weight: 600; margin: 0 0 8px;">%s</h1>
    <p style="color: #999; margin: 0; font-size: 14px;">%s</p>
    <p style="color: #666; margin-top: 24px; font-size: 12px;">local-compose proxy</p>
  </div>
</body>
</html>
`, html.EscapeString(title), status, html.EscapeString(title), html.EscapeString(detail))
	_, _ = w.Write([]byte(body))
}

// ServiceHosts returns the hostnames a service is reachable at (without the
// proxy port), in display order: the project URL first when the service is
// the project's default, then the service label, then each named port.
func ServiceHosts(project, serviceName string, svc config.Service, isDefault bool, domainSuffix string) []string {
	if svc.Ports == nil || len(svc.Ports.Entries) == 0 {
		return nil
	}
	suffix := project + "." + domainSuffix
	var hosts []string
	if isDefault {
		hosts = append(hosts, suffix)
	}
	label := serviceName
	if svc.Proxy != nil && svc.Proxy.Host != "" {
		label = svc.Proxy.Host
	}
	hosts = append(hosts, label+"."+suffix)
	for _, entry := range svc.Ports.Entries {
		if entry.Name != "" {
			hosts = append(hosts, entry.Name+"."+label+"."+suffix)
		}
	}
	return hosts
}
