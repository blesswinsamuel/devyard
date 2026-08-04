package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// FrameMaxLen caps a single frame body to avoid unbounded allocations on the
// receiving side. 16 MiB is far more than any expected log line or state
// snapshot but still small enough to be defensive.
const FrameMaxLen = 16 << 20

// RequestKind discriminates Request payloads sent from a client (CLI/TUI/web)
// to the supervisor.
type RequestKind string

const (
	KindList          RequestKind = "list"           // list services + status + pids
	KindLogs          RequestKind = "logs"           // stream a service's log file
	KindStop          RequestKind = "stop"           // stop every service in a project
	KindStopService   RequestKind = "stop_service"   // stop one service by name
	KindKillService   RequestKind = "kill_service"   // immediately signal one service (Signal; empty = SIGKILL)
	KindRestart       RequestKind = "restart"        // restart one (Service) or all services
	KindListProjects  RequestKind = "list_projects"  // list all known projects
	KindStartProject  RequestKind = "start_project"  // start a project from a config path
	KindStopProject   RequestKind = "stop_project"   // stop a project's services
	KindRemoveProject RequestKind = "remove_project" // stop a project and completely remove it from the daemon
	KindStopDaemon    RequestKind = "stop_daemon"    // stop all projects and shut down the daemon
	KindTop           RequestKind = "top"            // sample CPU/memory usage of one service (or all)
)

// Request is a client -> daemon message.
//
//   - Kind==KindLogs: Project + Service selects the log file, Follow enables tailing,
//     Previous selects the previous run's log instead of the current one.
//   - Kind==KindRestart: Project selects the project; Service selects one service
//     (empty means all).
//   - Kind==KindStopService: Project + Service selects the service to stop.
//   - Kind==KindKillService: Project + Service selects the service to kill;
//     Signal is the signal name (empty means SIGKILL).
//   - Kind==KindList / KindStop: Project selects the project.
//   - Kind==KindStartProject: ConfigPath is the absolute path to local-compose.yml;
//     Build runs pre-start builds; EnvFile is the absolute path to the env file
//     (empty means the daemon falls back to .env next to the config file).
//   - Kind==KindStopProject / KindStopDaemon: Project selects the project (or all
//     when empty for stop_daemon).
//   - Kind==KindTop: Project selects the project; Service selects one service
//     (empty means all). The daemon samples the service process groups twice
//     over a ~1s interval to derive CPU usage.
type Request struct {
	Kind       RequestKind `json:"kind"`
	Project    string      `json:"project,omitempty"`
	Service    string      `json:"service,omitempty"`
	Signal     string      `json:"signal,omitempty"`
	Follow     bool        `json:"follow,omitempty"`
	Previous   bool        `json:"previous,omitempty"`
	ConfigPath string      `json:"config_path,omitempty"`
	EnvFile    string      `json:"env_file,omitempty"`
	Build      bool        `json:"build,omitempty"`
}

// ResponseKind discriminates Response payloads sent from the supervisor to a
// client. A single request may produce many responses (e.g. a Logs follow
// stream emits KindLogLine frames followed by KindDone when the stream ends).
type ResponseKind string

const (
	KindStates     ResponseKind = "states"      // a snapshot of every service
	KindProjects   ResponseKind = "projects"    // a snapshot of every known project
	KindStats      ResponseKind = "stats"       // a per-service CPU/memory snapshot (KindTop)
	KindLogLine    ResponseKind = "log_line"    // one line of a service's log
	KindLogContent ResponseKind = "log_content" // bulk: entire existing log file text
	KindLogRotated ResponseKind = "log_rotated" // a new run started; the previous run's lines are over
	KindDone       ResponseKind = "done"        // request complete, no more frames
	KindError      ResponseKind = "error"       // an error occurred (Error has text)
)

// ServiceState is the wire form of a service snapshot. Time fields are encoded
// as RFC3339Nano strings (empty when zero) so the struct is self-contained
// JSON and decodable by non-Go clients (web UI).
type ServiceState struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	ExitCode   int    `json:"exit_code"`
	Restarts   int    `json:"restarts"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	HasHealth  bool   `json:"has_health"`
	Health     string `json:"health"`
}

// ProjectInfo is the wire form of a project snapshot. It describes one project
// known to the daemon, whether its supervisor is currently running, and the
// config path it was started from.
type ProjectInfo struct {
	Name            string `json:"name"`
	Status          string `json:"status"`      // running | stopped
	ConfigPath      string `json:"config_path"` // absolute path to local-compose.yml
	RunningServices int    `json:"running_services"`
	TotalServices   int    `json:"total_services"`
}

// ServiceStat is the wire form of a per-service resource snapshot returned by
// `top`. CPU is a percentage of one core averaged over the daemon's sampling
// interval and can exceed 100 for multi-core work; RSSBytes is the aggregate
// resident set size of every process in the service's process group. Procs is
// zero when the service has no live group, in which case CPU/RSS are zero too.
type ServiceStat struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	PID      int     `json:"pid"`
	PGID     int     `json:"pgid"`
	Procs    int     `json:"procs"`
	CPU      float64 `json:"cpu"`
	RSSBytes uint64  `json:"rss_bytes"`
}

// Response is a daemon -> client message.
type Response struct {
	Kind     ResponseKind   `json:"kind"`
	States   []ServiceState `json:"states,omitempty"`   // Kind==KindStates
	Projects []ProjectInfo  `json:"projects,omitempty"` // Kind==KindProjects
	Stats    []ServiceStat  `json:"stats,omitempty"`    // Kind==KindStats
	Project  string         `json:"project,omitempty"`  // Kind==KindLogLine (which project)
	Service  string         `json:"service,omitempty"`  // Kind==KindLogLine (which service)
	Line     string         `json:"line,omitempty"`     // Kind==KindLogLine
	Content  string         `json:"content,omitempty"`  // Kind==KindLogContent (bulk file text)
	Error    string         `json:"error,omitempty"`    // Kind==KindError
}

// WriteFrame writes v as a length-prefixed JSON frame: a 4-byte big-endian
// length header followed by the JSON body. It is safe to call from multiple
// goroutines only if the underlying writer is itself safe (a net.Conn is).
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("protocol: marshal: %w", err)
	}
	if len(body) > FrameMaxLen {
		return fmt.Errorf("protocol: frame too large: %d bytes", len(body))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("protocol: write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("protocol: write body: %w", err)
	}
	return nil
}

// ReadFrame reads a length-prefixed JSON frame into v. It returns an
// io.EOF-wrapped error when the peer closes the connection cleanly between
// frames.
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("protocol: read header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return fmt.Errorf("protocol: empty frame")
	}
	if n > FrameMaxLen {
		return fmt.Errorf("protocol: frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("protocol: read body: %w", err)
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("protocol: unmarshal: %w", err)
	}
	return nil
}

// FormatTime converts a time.Time to the wire RFC3339Nano form, returning an
// empty string for the zero time so omitted fields stay out of the JSON.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
