package harness

import (
	"errors"
	"regexp"
	"strconv"
	"time"

	"connectrpc.com/connect"
)

// RequireCode fails t unless err is a Connect error with code want.
func RequireCode(t TB, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %v error, got success", want)
		return
	}
	if got := connect.CodeOf(err); got != want {
		var ce *connect.Error
		msg := err.Error()
		if errors.As(err, &ce) {
			msg = ce.Message()
		}
		t.Fatalf("expected code %v, got %v: %s", want, got, msg)
	}
}

// NoError fails t on err.
func NoError(t TB, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// Start runs `devyard start args...` in the project dir and fails on error.
func (p *Project) Start(args ...string) Result {
	p.sb.t.Helper()
	return p.CLI(append([]string{"start"}, args...)...).MustSucceed(p.sb.t)
}

// WaitRunning waits until every named service (all when none) of p runs.
func (p *Project) WaitRunning(w *Watcher, names ...string) State {
	p.sb.t.Helper()
	return w.WaitForWithin(p.sb.t, 20*time.Second, "services of "+p.ID+" running", func(s State) bool {
		return s.AllRunning(p.ID, names...)
	})
}

// ServiceJSON is one element of `devyard status -o json` (protojson of
// v1.Service; int64 fields are omitted because protojson encodes them as
// strings).
type ServiceJSON struct {
	Project  string   `json:"project"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Health   string   `json:"health"`
	Pid      int      `json:"pid"`
	ExitCode int      `json:"exitCode"`
	Restarts int      `json:"restarts"`
	Message  string   `json:"message"`
	URLs     []string `json:"urls"`
}

// ProjectJSON is one element of `devyard project list -o json`.
type ProjectJSON struct {
	ID              string `json:"id"`
	ConfigPath      string `json:"configPath"`
	Status          string `json:"status"`
	Desired         string `json:"desired"`
	Error           string `json:"error"`
	ServicesTotal   int    `json:"servicesTotal"`
	ServicesRunning int    `json:"servicesRunning"`
}

// StatusJSON runs `devyard status -o json args...` in the project dir.
func (p *Project) StatusJSON(args ...string) []ServiceJSON {
	p.sb.t.Helper()
	r := p.CLI(append([]string{"status", "-o", "json"}, args...)...).MustSucceed(p.sb.t)
	var out []ServiceJSON
	r.JSON(p.sb.t, &out)
	return out
}

// FindService returns the named entry of a status list, or nil.
func FindService(list []ServiceJSON, name string) *ServiceJSON {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// MaxSeq returns the highest N in lines of the form "<prefix> N" in text
// (ticker output), or 0.
func MaxSeq(text, prefix string) int {
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + ` (\d+)\b`)
	max := 0
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// Seqs returns every N of "<prefix> N" in text, in order.
func Seqs(text, prefix string) []int {
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + ` (\d+)\b`)
	var out []int
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// CLIWithTimeout runs devyard in the project dir with an explicit timeout.
func (p *Project) CLIWithTimeout(d time.Duration, args ...string) Result {
	p.sb.t.Helper()
	return p.sb.CLIWith(RunOpts{Dir: p.Dir, Timeout: d}, args...)
}

// CLIEnv runs devyard in the project dir with extra environment entries.
func (p *Project) CLIEnv(env []string, args ...string) Result {
	p.sb.t.Helper()
	return p.sb.CLIWith(RunOpts{Dir: p.Dir, Env: env}, args...)
}
