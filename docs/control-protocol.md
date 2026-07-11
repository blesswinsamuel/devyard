# Control protocol

The wire protocol spoken between the global daemon and its clients
(`local-compose ps`/`logs`/`restart`/`down`, the TUI, and the web UI).
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
    Follow     bool        `json:"follow,omitempty"`
    ConfigPath string      `json:"config_path,omitempty"`
    Build      bool        `json:"build,omitempty"`
}
```

| `Kind` | `Project` | `Service` | Other | Behavior |
| --- | --- | --- | --- | --- |
| `"list"` | project name | — | — | Return one `states` response with a snapshot of every service in the project. |
| `"logs"` | project name | service name | `Follow` | Stream the service's log file. `follow:false` streams existing content and ends with `done`; `follow:true` keeps streaming new lines until shutdown/disconnect. |
| `"stop"` | project name | — | — | Stop every service in the project. Acks with `done` once all groups are torn down. |
| `"stop_service"` | project name | service name | — | Stop one service in place (no restart). Acks with `done`. |
| `"restart"` | project name | service name (empty = all) | — | Restart the named service, or all when empty. Acks with `done`. |
| `"list_projects"` | — | — | — | Return one `projects` response with a snapshot of all known projects. |
| `"start_project"` | — | — | `ConfigPath`, `Build` | Load the config at `ConfigPath` and start a supervisor for it. Acks with `done` or `error`. |
| `"stop_project"` | project name | — | — | Stop the named project's services and remove it from the daemon. Acks with `done`. |
| `"stop_daemon"` | — | — | — | Stop all projects and shut down the daemon. Acks with `done`. |

## Response (daemon -> client)

```go
type Response struct {
    Kind     ResponseKind   `json:"kind"`
    States   []ServiceState `json:"states,omitempty"`   // Kind == "states"
    Projects []ProjectInfo  `json:"projects,omitempty"` // Kind == "projects"
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
| `"log_content"` | Bulk: the entire existing log file text (`Content` populated; `Project`/`Service` identify the source). Sent once before streaming starts. |
| `"log_line"` | One line of a service's log (`Line` populated; `Project`/`Service` identify the source). Sent for each new line during follow. |
| `"done"` | Request complete; no more frames will follow on this connection. |
| `"error"` | An error occurred (`Error` has the message). The connection is now done. |

A single request may produce many responses. A `Logs{follow:true}` stream starts
with a single `log_content` frame (bulk existing content), then a sequence of
`log_line` frames for new lines, ending in `done` (on shutdown) or `error` (on
failure). A `Logs{follow:false}` stream sends a single `log_content` frame
followed by `done`. `list`/`stop`/`stop_service`/`restart` each produce a single
terminal `states`/`done`/`error`. `list_projects` produces a single `projects`
response. `start_project`/`stop_project`/`stop_daemon` produce a single `done`
or `error`.

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

## Client helpers (`control.Client`)

Prefer these over hand-rolling request/response loops:

- `client.List(project) ([]ServiceState, error)` — `list`.
- `client.Logs(project, service, follow, onLine)` — `logs`; calls `onLine` per
  line, returns on `done`/`error`.
- `client.Stop(project) error` — `stop` (stop all services in a project).
- `client.StopService(project, name) error` — `stop_service`.
- `client.Restart(project, service) error` — `restart` (empty service = all).
- `client.ListProjects() ([]ProjectInfo, error)` — `list_projects`.
- `client.StartProject(configPath, build) error` — `start_project`.
- `client.StopProject(project) error` — `stop_project`.
- `client.StopDaemon() error` — `stop_daemon`.

Each method sends one request and drains responses until a terminal frame. For
anything not covered (e.g. the TUI's long-lived follow that needs to be
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
    Restart(name string) error
    LogPath(name string) (string, error)
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
protocol serves the CLI, the TUI, and the web UI.
