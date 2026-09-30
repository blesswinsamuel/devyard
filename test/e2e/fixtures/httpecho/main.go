// Command httpecho is an e2e fixture HTTP server.
//
// It listens on 127.0.0.1:$PORT (or -port) and answers every request with
// "name=<$NAME> host=<Host> path=<path>". GET /health returns 200, unless
// $HEALTH_FILE is set, in which case it returns 200 only while that file
// exists (503 otherwise). GET /env?k=VAR returns the value of VAR.
// -delay postpones listening (a slow-starting service).
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	port := flag.String("port", os.Getenv("PORT"), "port to listen on")
	delay := flag.Duration("delay", 0, "wait before listening")
	flag.Parse()
	if *port == "" {
		fmt.Fprintln(os.Stderr, "httpecho: PORT not set")
		os.Exit(2)
	}
	name := os.Getenv("NAME")
	fmt.Printf("httpecho %s starting pid=%d\n", name, os.Getpid())
	time.Sleep(*delay)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if f := os.Getenv("HEALTH_FILE"); f != "" {
			if _, err := os.Stat(f); err != nil {
				http.Error(w, "unhealthy", http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/env", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, os.Getenv(r.URL.Query().Get("k")))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "name=%s host=%s path=%s\n", name, r.Host, r.URL.Path)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httpecho: listen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("httpecho %s listening on %s\n", name, ln.Addr())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		s := <-sig
		fmt.Printf("httpecho %s got %v\n", name, s)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "httpecho: %v\n", err)
		os.Exit(1)
	}
}
