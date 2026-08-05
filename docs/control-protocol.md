# Control protocol

The wire protocol spoken between the global daemon and its clients
(`local-compose ps`/`logs`/`restart`/`down`/`top` and the web UI).
Implementation: `internal/protocol/protocol.go`, `internal/control/server.go`,
`internal/control/client.go`.

## Transport

A Unix domain socket at
`$XDG_RUNTIME_DIR/local-compose/daemon.sock` (default `/tmp/local-compose/daemon.sock`).
One connection serves **one request**. Streaming requests (`Logs` with
`follow:true`) hold the connection open until the stream ends or the client
disconnects.

## Framing

Length-prefixed JSON frames:

```
[ 4-byte big-endian length ][ JSON body of <length> bytes ]
```

- `protocol.WriteFrame(w, v)` marshals `v` to JSON, prepends a 4-byte
  big-endian length, and writes both.
- `protocol.ReadFrame(r, v)` reads the header then exactly `<length>` bytes and
  unmarshals into `v`.
- A zero length is invalid; bodies larger than `protocol.FrameMaxLen`
  (16 MiB) are rejected on both read and write to avoid unbounded allocations.
- A clean peer-close between frames surfaces as an `io.EOF`-wrapped error from
  `ReadFrame`.

## Request (client -> daemon)

```go
type Request struct {
    Kind       RequestKind `json:"kind"`
    Project    string      `json:"project,omitempty"`
    Service    string      `json:"service,omitempty"`
    Signal     string      `json:"signal,omitempty"`
    Follow     bool        `json:"follow,omitempty"`
    Previous   bool        `json:"previous,omitempty"`
    Tail       int         `json:"tail,omitempty"` // last N lines of history; 0 = all (byte-capped)
    ConfigPath string      `json:"config_path,omitempty"`
    EnvFile    string      `json:"env_file,omitempty"`
    Build      bool        `json:"build,omitempty"`
}
```

| `Kind` | `Project` | `Service` | Other | Behavior |
| --- | --- | --- | --- | --- |
| `"list"` | project name | — | — | Return one `states` response with a snapshot of every service in the project. |
| `"logs"` | project name | service name | `Follow`, `Previous`, `Tail` | Stream the service's log file. `previous:true` streams the previous run's log (`<service>.prev.log`) instead of the current one; it errors with `no previous run` when none exists. `tail` limits the initial history dump to the last N lines (`0` / omitted = all, still capped at ~8 MiB of trailing content so the frame stays under `FrameMaxLen`). `follow:false` streams that history and ends with `done`; `follow:true` keeps streaming new lines until shutdown/disconnect. During follow, the stream transparently reopens the file when the supervisor rotates it (each spawn starts a fresh `<service>.log`, and size-based rotation uses the same slot), so it keeps following the live run across restarts; a `log_rotated` frame announces each new run. |
| `"stop"` | project name | — | — | Stop every service in the project. Acks with `done` once all groups are torn down. |
| `"stop_service"` | project name | service name | — | Stop one service in place (no restart). Acks with `done`. |
| `"kill_service"` | project name | service name (empty = all) | `Signal` | Signal one service (empty = all services, in start order) with the named signal; empty `Signal` means `SIGKILL`. No grace period. Acks with `done`. |
| `"restart"` | project name | service name (empty = all) | — | Restart the named service, or all when empty. Acks with `done`. |
| `"top"` | project name | service name (empty = all) | — | Sample the service's process group(s) twice over ~1s and return one `stats` response with per-service CPU/memory usage. |
| `"list_projects"` | — | — | — | Return one `projects` response with a snapshot of all known projects. |
| `"start_project"` | — | — | `ConfigPath`, `EnvFile`, `Build` | Load the config at `ConfigPath` (with env-file variables from `EnvFile`, or `.env` next to the config when empty) and start a supervisor for it. Acks with `done` or `error`. |
| `"stop_project"` | project name | — | — | Stop the named project's services. Acks with `done`. |
| `"remove_project"` | project name | — | — | Stop the named project's services, remove it from the daemon, and delete its runtime and state directories. Acks with `done`. |
| `"stop_daemon"` | — | — | — | Stop all projects and shut down the daemon. Acks with `done`. |

## Response (daemon -> client)

```go
type Response struct {
    Kind     ResponseKind   `json:"kind"`
    States   []ServiceState `json:"states,omitempty"`   // Kind == "states"
    Projects []ProjectInfo  `json:"projects,omitempty"` // Kind == "projects"
    Stats    []ServiceStat  `json:"stats,omitempty"`    // Kind == "stats"
    Project  string         `json:"project,omitempty"`   // Kind == "log_line" (which project)
    Service  string         `json:"service,omitempty"`  // Kind == "log_line" (which service)
    Line     string         `json:"line,omitempty"`     // Kind == "log_line"
    Content  string         `json:"content,omitempty"`  // Kind == "log_content" (bulk file text)
    Error    string         `json:"error,omitempty"`    // Kind == "error"
}
```

| `Kind` | Meaning |
| --- | --- |
| `"states"` | A snapshot of every service in a project (one response, `States` populated). |
| `"projects"` | A snapshot of every known project (`Projects` populated). |
| `"stats"` | A per-service CPU/memory snapshot (`Stats` populated). Sent in response to `"top"`. |
| `"log_content"` | Bulk: existing log history (`Content` populated; `Project`/`Service` identify the source). Sent once before streaming starts. May be a tailed/byte-capped window rather than the entire file. |
| `"log_line"` | One line of a service's log (`Line` populated; `Project`/`Service` identify the source). Sent for each new line during follow. |
| `"log_rotated"` | A new run started: the supervisor rotated the log (`<service>.log` → `<service>.prev.log`) and the previous run's lines are over. Carries `project` and `service` so multi-stream clients can route the reset. Frontends with a scrollback buffer (web) reset their view on receipt. |
| `"done"` | Request complete; no more frames will follow on this connection. |
| `"error"` | An error occurred (`Error` has the message). The connection is now done. |

A single request may produce many responses. A `Logs{follow:true}` stream starts
 with a single `log_content` frame (bulk existing content, optionally limited by
 `tail` and always byte-capped), then a sequence of `log_line` frames for new
 lines — with a `log_rotated` frame between runs when the service restarts —
 ending in `done` (on shutdown) or `error` (on failure). A `Logs{follow:false}`
  stream sends a single `log_content` frame followed by `done`. Foreground
  `up` sends `tail: 5000`
 (`protocol.DefaultLogTail`); CLI `logs` defaults to `0` (all within the byte
 cap) and accepts `--tail N`. `list`/`stop`/`stop_service`/`kill_service`/`restart`
 each produce a single terminal `states`/`done`/`error`. `list_projects` produces a
 single `projects` response. `top` produces a single `stats` response (the
 daemon blocks for ~1s sampling before sending it).
`start_project`/`stop_project`/`remove_project`/
`stop_daemon` produce a single `done` or `error`.

## ServiceState

```go
type ServiceState struct {
    Name       string `json:"name"`
    Status     string `json:"status"`        // starting | running | backoff | exited | stopped
    PID        int    `json:"pid"`
    ExitCode   int    `json:"exit_code"`
    Restarts   int    `json:"restarts"`
    StartedAt  string `json:"started_at,omitempty"`   // RFC3339Nano, UTC
    FinishedAt string `json:"finished_at,omitempty"`  // RFC3339Nano, UTC
    HasHealth  bool   `json:"has_health"`
    Health     string `json:"health"`                 // starting | healthy | unhealthy | "" (when !HasHealth)
}
```

Time fields are RFC3339Nano strings (UTC), empty when zero, so the struct is
self-contained JSON and decodable by non-Go clients (e.g. the web UI). Use
`protocol.FormatTime(t)` to produce them.

## ProjectInfo

```go
type ProjectInfo struct {
    Name       string `json:"name"`
    Status     string `json:"status"`      // running | stopped
    ConfigPath string `json:"config_path"` // absolute path to local-compose.yml
}
```

## ServiceStat

```go
type ServiceStat struct {
    Name     string  `json:"name"`
    Status   string  `json:"status"`
    PID      int     `json:"pid"`
    PGID     int     `json:"pgid"`
    Procs    int     `json:"procs"`     // live processes in the service's group; 0 when not running
    CPU      float64 `json:"cpu"`       // percent of one core over the ~1s sampling interval (can exceed 100)
    RSSBytes uint64  `json:"rss_bytes"` // aggregate resident set size of the group, in bytes
}
```

`CPU`/`RSSBytes`/`Procs` are zero for services with no live process group.

## Client helpers (`control.Client`)

Prefer these over hand-rolling request/response loops:

- `client.List(project) ([]ServiceState, error)` — `list`.
- `client.Logs(project, service, follow, previous, tail, onLine)` — `logs`;
  calls `onLine` per line, returns on `done`/`error`. `tail` is last N lines of
  history (`0` = all, byte-capped).
- `client.Stop(project) error` — `stop` (stop all services in a project).
- `client.StopService(project, name) error` — `stop_service`.
- `client.KillService(project, name, signal) error` — `kill_service` (empty
  `signal` = `SIGKILL`).
- `client.Restart(project, service) error` — `restart` (empty service = all).
- `client.Top(project, service) ([]ServiceStat, error)` — `top` (empty service
  = all); blocks ~1s while the daemon samples.
- `client.ListProjects() ([]ProjectInfo, error)` — `list_projects`.
- `client.StartProject(configPath, build) error` — `start_project`.
- `client.StopProject(project) error` — `stop_project`.
- `client.RemoveProject(project) error` — `remove_project`.
- `client.StopDaemon() error` — `stop_daemon`.

Each method sends one request and drains responses until a terminal frame. For
anything not covered (e.g. a long-lived follow that needs to be
cancelled mid-stream), use `client.Send` + `client.Recv` directly and close the
client to cancel.

## Backend interfaces (`control.MultiBackend` / `control.Backend`)

The server doesn't import the orchestrator; it depends on these interfaces, so
tests can drive it with a fake and so client-only packages don't pull the
supervisor transitively:

```go
// MultiBackend is the daemon-level interface (manages multiple projects).
type MultiBackend interface {
    ListProjects() []protocol.ProjectInfo
    StartProject(configPath string, build bool) error
    StopProject(name string) error
    StopDaemon() error
    ProjectBackend(project string) (Backend, error)
}

// Backend is the per-project interface (one supervisor).
type Backend interface {
    States() []protocol.ServiceState
    Stop(ctx context.Context) error
    StopService(name string) error
    KillService(name, signal string) error
    Restart(name string) error
    Top(name string) ([]protocol.ServiceStat, error)
    LogPath(name string) (string, error)
    PreviousLogPath(name string) (string, error)
}
```

`orchestrator.Daemon` implements `MultiBackend`.
`supervisor.NewControlBackend(*Supervisor)` adapts a `*Supervisor` to `Backend`.
`SingleProjectBackend` adapts a single `Backend` to `MultiBackend` (used by
tests).

## Adding a new request kind

1. Add a `KindXxx RequestKind` constant in `internal/protocol/protocol.go` and
   any new fields on `Request`/`Response` (with `omitempty`).
2. Add a method to the `Backend` or `MultiBackend` interface in
   `internal/control/control.go` and implement it on `*orchestrator.Daemon`
   (daemon-level) or `*Supervisor` via `supervisor/control_backend.go`
   (per-project).
3. Handle the new `RequestKind` in `control.Server`'s dispatch.
4. Add a `Client` helper (if it's a one-shot) or document the streaming
   contract (if it's multi-frame).
5. Update this doc and add unit tests in `internal/control/control_test.go`
   plus an integration case in `test/integration/` if user-facing.

Keep messages frontend-agnostic — no terminal-specific fields. The same
protocol serves the CLI and the web UI.
