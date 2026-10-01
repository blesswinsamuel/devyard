// Command sandbox starts a hermetic devyard daemon with a demo fixture
// project, prints one JSON line describing it, and blocks until SIGINT or
// SIGTERM, then tears everything down. Playwright's globalSetup uses it:
//
//	go run ./test/e2e/cmd/sandbox
//	{"webURL":"http://127.0.0.1:53211","dashboardURL":"http://...?token=...","proxyURL":"...","project":"demo","env":{...}}
//
// The env map is the sandbox environment, so callers can run the CLI
// against this daemon (`env ... devyard status`).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

const demoConfig = `name: demo
primary: web
services:
  web:
    run: {{fixture "httpecho"}}
    port: {{port "web"}}
    env:
      PORT: "{{port "web"}}"
      NAME: web
    ready:
      exec: "true"
      interval: 1s
  api:
    run: {{fixture "httpecho"}}
    ports:
      http: {{port "api"}}
    env:
      PORT: "{{port "api"}}"
      NAME: api
    depends_on: [web]
  worker:
    run: {{fixture "ticker"}} -interval 500ms -stderr
    env:
      NAME: worker
  crasher:
    run: {{fixture "exiter"}} -code 1 -after 2s
    restart: on-failure
  console:
    run: sh -c 'while read l; do echo "got: $l"; done'
    tty: true
tasks:
  greet: {{fixture "prompter"}}
  migrate:
    run: {{fixture "ticker"}} -interval 100ms -count 20
    tty: false
  fail: {{fixture "exiter"}} -code 3
`

// tb is a minimal harness.TB for use outside `go test`.
type tb struct {
	mu       sync.Mutex
	cleanups []func()
	failed   bool
}

func (t *tb) Helper()                         {}
func (t *tb) Name() string                    { return "sandbox" }
func (t *tb) Logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func (t *tb) Errorf(format string, args ...any) {
	t.mu.Lock()
	t.failed = true
	t.mu.Unlock()
	t.Logf(format, args...)
}
func (t *tb) Failed() bool     { t.mu.Lock(); defer t.mu.Unlock(); return t.failed }
func (t *tb) Cleanup(f func()) { t.mu.Lock(); t.cleanups = append(t.cleanups, f); t.mu.Unlock() }
func (t *tb) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	t.runCleanups()
	os.Exit(1)
}

func (t *tb) runCleanups() {
	t.mu.Lock()
	fns := t.cleanups
	t.cleanups = nil
	t.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

func main() {
	keep := flag.Bool("keep", false, "keep the sandbox dirs on exit")
	noProject := flag.Bool("no-project", false, "start the daemon without the demo project")
	flag.Parse()

	setupCleanup, err := harness.Setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
	t := &tb{}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	sb := harness.New(t)
	if *keep {
		sb.KeepOnExit()
	}
	project := ""
	if !*noProject {
		p := sb.WriteProject("demo", demoConfig, nil)
		p.CLI("start").MustSucceed(t)
		project = p.ID
	}
	d := sb.Daemon()
	env := map[string]string{}
	for _, kv := range sb.Env() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	bin, _ := harness.DevyardPath()
	out, _ := json.Marshal(map[string]any{
		"webURL":       d.WebURL(),
		"dashboardURL": d.WebURLFromCLI(),
		"proxyURL":     d.ProxyURL(),
		"socket":       sb.SocketPath(),
		"project":      project,
		"devyard":      bin,
		"root":         sb.Root,
		"env":          env,
		"goos":         runtime.GOOS,
	})
	fmt.Println(string(out))

	<-sig
	fmt.Fprintln(os.Stderr, "sandbox: shutting down")
	t.runCleanups()
	setupCleanup()
	if t.Failed() {
		os.Exit(1)
	}
}
