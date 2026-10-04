package daemon_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/blesswinsamuel/devyard/test/e2e/harness"
)

func writeGlobalConfig(t *testing.T, sb *harness.Sandbox, content string) {
	t.Helper()
	if err := os.WriteFile(sb.GlobalConfigPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const liveTicker = `services:
  a:
    run: {{fixture "ticker"}} -interval 200ms
`

// Editing the project list by hand adds, removes and reorders projects with
// no daemon restart.
func TestGlobalConfigLive_ProjectListEdits(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	one := sb.WriteProject("liveone", liveTicker, nil)
	two := sb.WriteProject("livetwo", liveTicker, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())

	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"projects: ["+one.Dir+", "+two.Dir+"]\n")
	w.WaitFor(t, "both projects appear in order", func(s harness.State) bool {
		a, b := s.Project("liveone"), s.Project("livetwo")
		return a != nil && b != nil && a.GetPosition() == 0 && b.GetPosition() == 1
	})
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"projects: ["+two.Dir+", "+one.Dir+"]\n")
	w.WaitFor(t, "order swapped", func(s harness.State) bool {
		return s.Project("livetwo").GetPosition() == 0 && s.Project("liveone").GetPosition() == 1
	})
	// A running project is stopped and forgotten when its entry is deleted.
	sb.CLI("project", "start", "liveone").MustSucceed(t)
	one.WaitRunning(w)
	pid := w.State().ServicePid("liveone", "a")
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"projects: ["+two.Dir+"]\n")
	w.WaitFor(t, "liveone removed", func(s harness.State) bool { return s.Project("liveone") == nil })
	harness.Eventually(t, "liveone's process is gone", func(c *harness.C) {
		if syscall.Kill(pid, 0) == nil {
			c.Errorf("process %d still alive", pid)
		}
	})
	if out := sb.CLI("project", "list").MustSucceed(t).Stdout; strings.Contains(out, "liveone") {
		t.Fatalf("project list still shows liveone:\n%s", out)
	}
}

// A config that does not parse changes nothing and is reported; fixing it
// clears the report.
func TestGlobalConfigLive_InvalidConfigKeepsTheLastGoodOne(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	p := sb.WriteProject("livekeep", liveTicker, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"projects: ["+p.Dir+"]\n")
	w.WaitFor(t, "project appears", func(s harness.State) bool { return s.Project("livekeep") != nil })

	writeGlobalConfig(t, sb, "projects: [unterminated\nweb: {port: nope\n")
	harness.Eventually(t, "config error reported", func(c *harness.C) {
		if msg := d.Info().GetConfigError(); msg == "" {
			c.Errorf("no config_error in daemon info")
		}
	})
	if w.State().Project("livekeep") == nil {
		t.Fatal("a broken config removed the project")
	}
	// Out-of-range values are invalid too.
	writeGlobalConfig(t, sb, "web:\n  port: 70000\n")
	harness.Eventually(t, "range error reported", func(c *harness.C) {
		if msg := d.Info().GetConfigError(); !strings.Contains(msg, "web.port") {
			c.Errorf("config_error = %q", msg)
		}
	})
	writeGlobalConfig(t, sb, harness.DefaultGlobalConfig+"projects: ["+p.Dir+"]\n")
	harness.Eventually(t, "config error cleared", func(c *harness.C) {
		if msg := d.Info().GetConfigError(); msg != "" {
			c.Errorf("config_error = %q", msg)
		}
	})
}

// The web listener moves to a new port without a restart; a port that cannot
// be bound leaves the old listener serving and is reported.
func TestGlobalConfigLive_WebListenerRebinds(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	old := d.Info().GetWebAddr()
	reply := func(addr string) bool {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	if !reply(old) {
		t.Fatalf("dashboard does not answer on %s", old)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	writeGlobalConfig(t, sb, fmt.Sprintf("web:\n  host: 127.0.0.1\n  port: %d\nproxy:\n  host: 127.0.0.1\n  port: 0\n", busyPort))
	harness.Eventually(t, "bind failure reported", func(c *harness.C) {
		if msg := d.Info().GetConfigError(); !strings.Contains(msg, "web dashboard") {
			c.Errorf("config_error = %q", msg)
		}
	})
	if got := d.Info().GetWebAddr(); got != old || !reply(old) {
		t.Fatalf("the old listener must keep serving: addr=%s", got)
	}

	free := harness.FreePort(t)
	writeGlobalConfig(t, sb, fmt.Sprintf("web:\n  host: 127.0.0.1\n  port: %d\nproxy:\n  host: 127.0.0.1\n  port: 0\n", free))
	want := fmt.Sprintf("127.0.0.1:%d", free)
	harness.Eventually(t, "dashboard moved", func(c *harness.C) {
		if got := d.Info().GetWebAddr(); got != want {
			c.Errorf("web_addr = %q, want %q", got, want)
		} else if !reply(want) {
			c.Errorf("nothing answers on %s", want)
		}
	})
	if msg := d.Info().GetConfigError(); msg != "" {
		t.Fatalf("config_error after a good bind: %q", msg)
	}
	harness.Eventually(t, "old port released", func(c *harness.C) {
		if reply(old) {
			c.Errorf("%s still answers", old)
		}
	}, harness.Within(15_000_000_000))
}

// The proxy domain changes live and the URLs of services follow.
func TestGlobalConfigLive_ProxyDomainUpdatesURLs(t *testing.T) {
	t.Parallel()
	sb := harness.New(t, harness.WithGlobalConfig("web:\n  host: 127.0.0.1\n  port: 0\nproxy:\n  host: 127.0.0.1\n  port: 0\n  domain_suffix: one.test\n"))
	p := sb.WriteProject("liveurl", `services:
  a:
    run: {{fixture "ticker"}} -interval 200ms
    port: auto
`, nil)
	d := sb.Daemon()
	w := d.Watch(context.Background())
	sb.CLI("add", p.Dir, "--no-start").MustSucceed(t)
	hasSuffix := func(s harness.State, suffix string) bool {
		urls := s.Service("liveurl", "a").GetUrls()
		return len(urls) > 0 && strings.Contains(urls[0], suffix)
	}
	w.WaitFor(t, "urls under one.test", func(s harness.State) bool { return hasSuffix(s, ".liveurl.one.test") })
	oldProxy := d.Info().GetProxyAddr()

	cfg, err := os.ReadFile(sb.GlobalConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, sb, strings.Replace(string(cfg), "one.test", "two.test", 1))
	w.WaitFor(t, "urls under two.test", func(s harness.State) bool { return hasSuffix(s, ".liveurl.two.test") })
	if msg := d.Info().GetConfigError(); msg != "" {
		t.Fatalf("config_error: %q", msg)
	}
	if got := d.Info().GetDomainSuffix(); got != "two.test" {
		t.Fatalf("domain_suffix = %q", got)
	}
	harness.Eventually(t, "old proxy listener closed", func(c *harness.C) {
		if conn, err := net.Dial("tcp", oldProxy); err == nil {
			_ = conn.Close()
			c.Errorf("%s still accepts connections", oldProxy)
		}
	})
}

// allowed_hosts applies without a restart.
func TestGlobalConfigLive_AllowedHosts(t *testing.T) {
	t.Parallel()
	sb := harness.New(t)
	d := sb.Daemon()
	hc := d.WebClient()
	port := d.WebHost()[strings.LastIndexByte(d.WebHost(), ':'):]
	status := func(host string) int {
		return harness.HTTPDo(hc, http.MethodPost, d.WebURL()+"/devyard.v1.DaemonService/GetDaemon", host+port,
			http.Header{"Content-Type": {"application/json"}}, strings.NewReader("{}")).Code
	}
	if got := status("late.test"); got != http.StatusForbidden {
		t.Fatalf("before: %d, want 403", got)
	}
	writeGlobalConfig(t, sb, "web:\n  host: 127.0.0.1\n  port: 0\n  allowed_hosts: [late.test]\nproxy:\n  host: 127.0.0.1\n  port: 0\n")
	harness.Eventually(t, "host allowed", func(c *harness.C) {
		if got := status("late.test"); got == http.StatusForbidden {
			c.Errorf("late.test still gets 403")
		}
	})
}
