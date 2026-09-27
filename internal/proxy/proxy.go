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
	"crypto/tls"
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

	"github.com/blesswinsamuel/devyard/internal/config"
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

// ServerOptions configures the reverse proxy server.
type ServerOptions struct {
	Addr             string
	DomainSuffix     string
	Resolver         Resolver
	TLS              TLSOptions
	DashboardHandler http.Handler
}

// TLSOptions configures TLS on the reverse proxy server.
type TLSOptions struct {
	Enabled      bool
	Addr         string      // e.g. "127.0.0.1:8443"
	TLSConfig    *tls.Config // TLS configuration (e.g. from CertManager)
	HTTPRedirect bool        // Redirect plain HTTP requests to HTTPS
}

// Server is the reverse-proxy HTTP server owned by the daemon process.
type Server struct {
	opts ServerOptions

	mu           sync.Mutex
	httpListener net.Listener
	tlsListener  net.Listener
}

// NewServer creates a proxy server using ServerOptions.
func NewServer(opts ServerOptions) *Server {
	return &Server{
		opts: ServerOptions{
			Addr:             opts.Addr,
			DomainSuffix:     strings.ToLower(opts.DomainSuffix),
			Resolver:         opts.Resolver,
			TLS:              opts.TLS,
			DashboardHandler: opts.DashboardHandler,
		},
	}
}

// ListenAndServe starts serving. It returns once the listeners are ready.
// Call Close to stop accepting connections.
func (s *Server) ListenAndServe() error {
	if s.opts.Addr == "" && (!s.opts.TLS.Enabled || s.opts.TLS.Addr == "") {
		return errors.New("proxy: no address configured to listen on")
	}

	if s.opts.Addr != "" {
		ln, err := net.Listen("tcp", s.opts.Addr)
		if err != nil {
			return fmt.Errorf("proxy: listen %s: %w", s.opts.Addr, err)
		}
		s.mu.Lock()
		s.httpListener = ln
		s.mu.Unlock()
		slog.Info("proxy listening http", "addr", s.Addr(), "domain_suffix", s.opts.DomainSuffix)

		go func() {
			var handler http.Handler = s
			if s.opts.TLS.Enabled && s.opts.TLS.HTTPRedirect {
				handler = http.HandlerFunc(s.redirectHTTPToHTTPS)
			}
			httpSrv := &http.Server{Handler: handler}
			_ = httpSrv.Serve(ln)
		}()
	}

	if s.opts.TLS.Enabled && s.opts.TLS.Addr != "" {
		ln, err := net.Listen("tcp", s.opts.TLS.Addr)
		if err != nil {
			if s.httpListener != nil {
				_ = s.httpListener.Close()
			}
			return fmt.Errorf("proxy: listen tls %s: %w", s.opts.TLS.Addr, err)
		}

		tlsCfg := s.opts.TLS.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		tlsLn := tls.NewListener(ln, tlsCfg)
		s.mu.Lock()
		s.tlsListener = tlsLn
		s.mu.Unlock()
		slog.Info("proxy listening https", "addr", s.TLSAddr(), "domain_suffix", s.opts.DomainSuffix)

		go func() {
			httpsSrv := &http.Server{Handler: s}
			_ = httpsSrv.Serve(tlsLn)
		}()
	}

	return nil
}

func (s *Server) redirectHTTPToHTTPS(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	tlsPort := 443
	if _, portStr, err := net.SplitHostPort(s.TLSAddr()); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			tlsPort = p
		}
	}
	targetHost := host
	if tlsPort != 443 {
		targetHost = net.JoinHostPort(host, strconv.Itoa(tlsPort))
	}
	target := "https://" + targetHost + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

// Addr returns the actual HTTP listener address (useful when port 0 is used).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpListener == nil {
		return s.opts.Addr
	}
	return s.httpListener.Addr().String()
}

// TLSAddr returns the actual HTTPS listener address (useful when port 0 is used).
func (s *Server) TLSAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tlsListener == nil {
		return s.opts.TLS.Addr
	}
	return s.tlsListener.Addr().String()
}

// Close stops both listeners. It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	httpLn := s.httpListener
	tlsLn := s.tlsListener
	s.httpListener = nil
	s.tlsListener = nil
	s.mu.Unlock()

	var errs []error
	if httpLn != nil {
		if err := httpLn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if tlsLn != nil {
		if err := tlsLn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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

	if s.opts.DashboardHandler != nil {
		if host == "localhost" || host == "127.0.0.1" || host == "devyard" || (s.opts.DomainSuffix != "" && host == "devyard."+s.opts.DomainSuffix) {
			s.opts.DashboardHandler.ServeHTTP(w, r)
			return
		}
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
			fmt.Sprintf("The service behind %s is not running (status: %s). Start it with `devyard start %s`.", host, status, route.Service))
		return
	}
	s.proxyFor(route).ServeHTTP(w, r)
}

// routeIndex builds the host→route map from the resolver's live routes.
// Routes are considered in resolver order; the first route for a host wins.
func (s *Server) routeIndex() map[string]Route {
	routes := s.opts.Resolver.ProxyRoutes()
	idx := make(map[string]Route, len(routes))
	for _, route := range routes {
		for _, host := range routeHosts(route, s.opts.DomainSuffix) {
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
<head><meta charset="utf-8"><title>%s — devyard</title></head>
<body style="font-family: ui-sans-serif, system-ui, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #111; color: #eee;">
  <div style="text-align: center; padding: 2rem;">
    <div style="font-size: 48px; margin-bottom: 8px;">%d</div>
    <h1 style="font-size: 20px; font-weight: 600; margin: 0 0 8px;">%s</h1>
    <p style="color: #999; margin: 0; font-size: 14px;">%s</p>
    <p style="color: #666; margin-top: 24px; font-size: 12px;">devyard proxy</p>
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
