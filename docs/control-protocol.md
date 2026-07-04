# Control protocol

The wire protocol spoken between the supervisor and its clients
(`local-compose ps`/`logs`/`restart`/`down`, the TUI, and the future web UI).
Implementation: `internal/protocol/protocol.go`, `internal/control/server.go`,
`internal/control/client.go`.

## Transport

A Unix domain socket at
`$XDG_RUNTIME_DIR/local-compose/<project>/supervisor.sock`. One connection
serves **one request**. Streaming requests (`Logs` with `follow:true`) hold the
connection open until the stream ends or the client disconnects.

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

## Request (client -> supervisor)

```go
type Request struct {
    Kind    RequestKind `json:"kind"`
    Service string      `json:"service,omitempty"`
    Follow  bool        `json:"follow,omitempty"`
}
```

| `Kind` | `Service` | `Follow` | Behavior |
| --- | --- | --- | --- |
| `"list"` | — | — | Return one `states` response with a snapshot of every service. |
| `"logs"` | service name | bool | Stream the service's log file. `follow:false` streams existing content and ends with `done`; `follow:true` keeps streaming new lines until shutdown/disconnect. |
| `"stop"` | — | — | Stop every service (`down`). Acks with `done` once all groups are torn down. |
| `"stop_service"` | service name | — | Stop one service in place (no restart). Acks with `done`. |
| `"restart"` | service name (empty = all) | — | Restart the named service, or all when empty. Acks with `done`. |

## Response (supervisor -> client)

```go
type Response struct {
    Kind   ResponseKind   `json:"kind"`
    States []ServiceState `json:"states,omitempty"` // Kind == "states"
    Line   string         `json:"line,omitempty"`   // Kind == "log_line"
    Error  string         `json:"error,omitempty"`  // Kind == "error"
}
```

| `Kind` | Meaning |
| --- | --- |
| `"states"` | A snapshot of every service (one response, `States` populated). |
| `"log_line"` | One line of a service's log (`Line` populated). |
| `"done"` | Request complete; no more frames will follow on this connection. |
| `"error"` | An error occurred (`Error` has the message). The connection is now done. |

A single request may produce many responses. A `Logs{follow:true}` stream is a
sequence of `log_line` frames ending in `done` (on shutdown) or `error` (on
failure). `list`/`stop`/`stop_service`/`restart` each produce a single terminal
`states`/`done`/`error`.

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

## Client helpers (`control.Client`)

Prefer these over hand-rolling request/response loops:

- `client.List() ([]ServiceState, error)` — `list`.
- `client.Logs(service, follow, onLine)` — `logs`; calls `onLine` per line,
  returns on `done`/`error`.
- `client.Stop()` — `stop` (`down`).
- `client.StopService(name)` — `stop_service`.
- `client.Restart(service)` — `restart` (empty service = all).

Each method sends one request and drains responses until a terminal frame. For
anything not covered (e.g. the TUI's long-lived follow that needs to be
cancelled mid-stream), use `client.Send` + `client.Recv` directly and close the
client to cancel.

## Backend interface (`control.Backend`)

The server doesn't import the supervisor; it depends on this interface, so
tests can drive it with a fake and so client-only packages don't pull the
supervisor transitively:

```go
type Backend interface {
    States() []protocol.ServiceState
    Stop(ctx context.Context) error
    StopService(name string) error
    Restart(name string) error
    LogPath(name string) (string, error)
}
```

`internal/supervisor/control_backend.go` adapts `*Supervisor` to `Backend`.

## Adding a new request kind

1. Add a `KindXxx RequestKind` constant in `internal/protocol/protocol.go` and
   any new fields on `Request`/`Response` (with `omitempty`).
2. Add a `Backend` method in `internal/control/control.go` (the `Backend`
   interface) and implement it on `*Supervisor` in
   `internal/supervisor/control_backend.go`.
3. Handle the new `RequestKind` in `control.Server`'s dispatch.
4. Add a `Client` helper (if it's a one-shot) or document the streaming
   contract (if it's multi-frame).
5. Update this doc and add unit tests in `internal/control/control_test.go`
   plus an integration case in `test/integration/` if user-facing.

Keep messages frontend-agnostic — no terminal-specific fields. The same
protocol serves the CLI, the TUI, and the web UI.
