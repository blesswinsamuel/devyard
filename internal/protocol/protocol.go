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

// DefaultLogTail is the history window interactive frontends (web) and
// foreground `up` request. CLI `logs` defaults to 0 (all, subject to the
// server's byte cap).
const DefaultLogTail = 5000

// RequestKind discriminates Request payloads sent from a client (CLI/web)
// to the supervisor.
type RequestKind string

const (
	KindList             RequestKind = "list"               // list services + status + pids
	KindLogs             RequestKind = "logs"               // stream a service's log file
	KindStop             RequestKind = "stop"               // stop every service in a project
	KindStopService      RequestKind = "stop_service"       // stop one service by name
	KindStartService     RequestKind = "start_service"      // start one service by name (resumes in place or lazy-starts on a stopped project)
	KindKillService      RequestKind = "kill_service"       // immediately signal one service (Signal; empty = SIGKILL)
	KindRestart          RequestKind = "restart"            // restart one (Service) or all services
	KindListProjects     RequestKind = "list_projects"      // list all known projects
	KindStartProject     RequestKind = "start_project"      // start a project from a config path
	KindStopProject      RequestKind = "stop_project"       // stop a project's services
	KindRemoveProject    RequestKind = "remove_project"     // stop a project and completely remove it from the daemon
	KindStopDaemon       RequestKind = "stop_daemon"        // stop all projects and shut down the daemon
	KindTop              RequestKind = "top"                // sample CPU/memory usage of one service (or all)
	KindRunAction        RequestKind = "run_action"         // run a one-off action
	KindListActions      RequestKind = "list_actions"       // list defined actions for a project
	KindListActionStates RequestKind = "list_action_states" // list action runtime states for a project
	KindGitLog           RequestKind = "git_log"            // list the git commit log for a project
	KindGitDiff          RequestKind = "git_diff"           // get diff and changed files for a commit
	KindGitCommit        RequestKind = "git_commit"         // create a new git commit for uncommitted changes
	KindGitStage         RequestKind = "git_stage"          // stage or unstage files
)

// Request is a client -> daemon message.
//
//   - Kind==KindLogs: Project + Service selects the log file, Follow enables tailing,
//     Previous selects the previous run's log instead of the current one,
//     Tail limits history to the last N lines (0 = all, subject to a byte cap).
//   - Kind==KindRestart: Project selects the project; Service selects one service
//     (empty means all).
//   - Kind==KindStopService / KindStartService: Project + Service selects the
//     service to stop or start.
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
//   - Kind==KindRunAction: Project + Action selects the action to run; Args contains extra CLI flags/args.
//   - Kind==KindListActions: Project selects the project.
//   - Kind==KindListActionStates: Project selects the project.
//   - Kind==KindGitLog: Project selects the project; its commit log is returned.
//   - Kind==KindGitDiff: Project selects the project; Hash selects the commit hash (empty defaults to HEAD); Path optional file path filter.
//   - Kind==KindGitCommit: Project selects the project; Message is the commit message.
//   - Kind==KindGitStage: Project selects the project; Path selects file; Unstage selects unstage action; StageAll targets all files.
type Request struct {
	Kind          RequestKind `json:"kind"`
	Project       string      `json:"project,omitempty"`
	Service       string      `json:"service,omitempty"`
	Action        string      `json:"action,omitempty"`
	Args          []string    `json:"args,omitempty"`
	Signal        string      `json:"signal,omitempty"`
	Follow        bool        `json:"follow,omitempty"`
	Previous      bool        `json:"previous,omitempty"`
	Tail          int         `json:"tail,omitempty"` // last N lines of history; 0 = all (byte-capped)
	ConfigPath    string      `json:"config_path,omitempty"`
	EnvFile       string      `json:"env_file,omitempty"`
	Build         bool        `json:"build,omitempty"`
	RemoveOrphans *bool       `json:"remove_orphans,omitempty"`
	Hash          string      `json:"hash,omitempty"`
	Path          string      `json:"path,omitempty"`
	Message       string      `json:"message,omitempty"`
	Unstage       bool        `json:"unstage,omitempty"`
	StageAll      bool        `json:"stage_all,omitempty"`
	ContextLines  int         `json:"context_lines,omitempty"`
}

// ResponseKind discriminates Response payloads sent from the supervisor to a
// client. A single request may produce many responses (e.g. a Logs follow
// stream emits KindLogLine frames followed by KindDone when the stream ends).
type ResponseKind string

const (
	KindStates       ResponseKind = "states"        // a snapshot of every service
	KindProjects     ResponseKind = "projects"      // a snapshot of every known project
	KindActions      ResponseKind = "actions"       // a list of defined actions
	KindActionStates ResponseKind = "action_states" // a snapshot of action runtime states
	KindStats        ResponseKind = "stats"         // a per-service CPU/memory snapshot (KindTop)
	KindGitCommits   ResponseKind = "git_commits"   // the git commit log for a project (KindGitLog)
	KindGitDiffData  ResponseKind = "git_diff"      // commit diff and changed files (KindGitDiff)
	KindLogLine      ResponseKind = "log_line"      // one line of a service's log or action output
	KindLogContent   ResponseKind = "log_content"   // bulk: existing log history (possibly tailed)
	KindLogRotated   ResponseKind = "log_rotated"   // a new run started; the previous run's lines are over
	KindDone         ResponseKind = "done"          // request complete, no more frames
	KindError        ResponseKind = "error"         // an error occurred (Error has text)
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

// ActionInfo is the wire form of a project action.
type ActionInfo struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	WorkingDir string   `json:"working_dir,omitempty"`
	TTY        bool     `json:"tty,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
}

// ActionState is the wire form of an action's runtime state. Its shape mirrors
// ServiceState (status/pid/exit_code/times) so frontends can reuse the same
// status-dot and label helpers.
type ActionState struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	ExitCode   int    `json:"exit_code"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
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

// GitCommit is the wire form of one commit in a project's git log. Time is
// encoded as an RFC3339 string (see FormatTime).
type GitCommit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Time    string   `json:"time"`
	Parents []string `json:"parents,omitempty"`
	Subject string   `json:"subject"`
	Head    bool     `json:"head,omitempty"` // true when this commit is the current HEAD
}

// GitFileChange describes one modified/added/deleted/renamed file in a commit.
type GitFileChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"` // "M", "A", "D", "R", etc.
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Staged    bool   `json:"staged,omitempty"`
	Unstaged  bool   `json:"unstaged,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`
}

// GitDiffResult is the wire payload returned for a commit diff query.
type GitDiffResult struct {
	Commit GitCommit       `json:"commit"`
	Files  []GitFileChange `json:"files"`
	Diff   string          `json:"diff"`
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
	Kind           ResponseKind   `json:"kind"`
	States         []ServiceState `json:"states,omitempty"`           // Kind==KindStates
	Projects       []ProjectInfo  `json:"projects,omitempty"`         // Kind==KindProjects
	Actions        []ActionInfo   `json:"actions,omitempty"`          // Kind==KindActions
	ActionStates   []ActionState  `json:"action_states,omitempty"`    // Kind==KindActionStates
	Stats          []ServiceStat  `json:"stats,omitempty"`            // Kind==KindStats
	GitCommits     []GitCommit    `json:"git_commits,omitempty"`      // Kind==KindGitCommits
	GitDiff        *GitDiffResult `json:"git_diff,omitempty"`         // Kind==KindGitDiff
	Project        string         `json:"project,omitempty"`          // Kind==KindLogLine (which project)
	Service        string         `json:"service,omitempty"`          // Kind==KindLogLine (which service)
	Action         string         `json:"action,omitempty"`           // Kind==KindLogLine (which action)
	Line           string         `json:"line,omitempty"`             // Kind==KindLogLine
	Content        string         `json:"content,omitempty"`          // Kind==KindLogContent (bulk file text)
	ActionExitCode *int           `json:"action_exit_code,omitempty"` // Kind==KindDone for KindRunAction
	Error          string         `json:"error,omitempty"`            // Kind==KindError
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
