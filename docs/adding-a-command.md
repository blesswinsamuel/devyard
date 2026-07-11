# Adding a CLI command

A recipe for adding a new `local-compose <command>` end to end, following how
the existing commands are wired. Use this as a checklist.

## Decide what kind of command it is

- **Control command** (talks to a running daemon): `ps`, `logs`, `restart`,
  `down` — these dial the daemon's control socket and send a request. You'll
  need a new protocol kind; see [control-protocol.md](control-protocol.md) >
  "Adding a new request kind".
- **Local command** (no daemon needed): `build` — runs against the config
  directly. No protocol change.
- **Frontend command**: `tui`, and the web UI — long-running clients of the
  control socket.
- **Daemon-management command**: `start-daemon`, `stop-daemon` — manage the
  global daemon process itself.

## Steps (control command example: `local-compose pause <service>`)

### 1. Protocol

In `internal/protocol/protocol.go`:

```go
const KindPause RequestKind = "pause"
```

Add fields on `Request` only if needed (here, `Service`). Update
[docs/control-protocol.md](control-protocol.md).

### 2. Backend + supervisor

Add to the `Backend` interface in `internal/control/control.go`:

```go
Pause(name string) error
```

Implement it on `*Supervisor` in `internal/supervisor/control_backend.go`
and/or `supervisor.go`. Add a unit test in `internal/supervisor/`.

### 3. Server dispatch

In `internal/control/server.go`, add a case for `protocol.KindPause` in the
request handler that calls `backend.Pause(req.Service)` and writes a `done`
(or `error`) frame. The server already resolves the per-project `Backend` via
`MultiBackend.ProjectBackend(req.Project)`.

### 4. Client helper

In `internal/control/client.go`:

```go
func (c *Client) Pause(project, service string) error {
    if err := c.Send(protocol.Request{Kind: protocol.KindPause, Project: project, Service: service}); err != nil {
        return err
    }
    return c.awaitDone()
}
```

Add a case to `internal/control/control_test.go` using a fake `Backend`.

### 5. CLI command

Create `internal/cli/pause.go` mirroring `restart.go` (the closest analogue):

```go
var pauseCmd = &cobra.Command{
    Use:   "pause [service]",
    Short: "Pause one or all services",
    Args:  cobra.MaximumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, err := loadConfig(flagConfigPath, flagProject)
        if err != nil {
            return err
        }
        service := ""
        if len(args) == 1 {
            service = args[0]
        }
        socket, err := dialDaemon()
        if err != nil {
            fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
            return err
        }
        client, err := control.Dial(socket)
        if err != nil {
            fmt.Fprintf(os.Stderr, "local-compose: no daemon running\n")
            return err
        }
        defer func() { _ = client.Close() }()
        if err := client.Pause(cfg.Project, service); err != nil {
            return err
        }
        fmt.Fprintf(os.Stderr, "local-compose: paused %q\n", service)
        return nil
    },
}
```

Register it in `internal/cli/root.go`:

```go
rootCmd.AddCommand(pauseCmd)
```

### 6. Tests + docs

- Unit test in `internal/control/control_test.go` (fake backend) and
  `internal/supervisor/` (real behavior).
- Add an integration case in `test/integration/e2e_test.go` if the command is
  user-facing and observable through the binary.
- Add a row to the Commands table in [README.md](../README.md).
- Add a row to the request-kind table in
  [docs/control-protocol.md](control-protocol.md).

## Patterns to copy

| You're adding... | Copy |
| --- | --- |
| A control one-shot (`restart`, `down`) | `internal/cli/restart.go`, `down.go` |
| A streaming command (`logs`) | `internal/cli/logs.go` + `client.Logs` |
| A local command (`build`) | `internal/cli/build.go` (no socket) |
| A frontend (`tui`) | `internal/cli/tui.go` + `internal/tui/` |
| A daemon-management command | `internal/cli/start_daemon.go`, `stop_daemon.go` |

## Key helpers

| Helper | What it does |
| --- | --- |
| `loadConfig(flagConfigPath, flagProject)` | Finds, parses, validates the config, derives project name, computes topo order. |
| `ensureDaemon()` | Checks if the daemon is running, spawns it if not, returns the socket path. Used by `up`. |
| `dialDaemon()` | Dials the daemon's control socket. Returns an error if the daemon is not running. Used by `ps`, `logs`, `restart`, `down`. |
| `control.Dial(socket)` | Opens a connection to the control socket. |

## Gotchas

- **`-f` is reserved** as the persistent `--file` flag, so don't use `-f` as a
  shorthand for any new command flag.
- **Always `loadConfig` + `dialDaemon`** (or `ensureDaemon` for `up`) in the
  command, then `control.Dial(socket)`. Don't invent a second way to find the
  socket.
- **Close the client** (`defer client.Close()`).
- **`loadConfig`** already finds the config (walking up from cwd), validates it,
  derives the project name, and computes the topological `Order` — reuse it,
  don't re-derive.
- If the daemon child needs to inherit a new flag, handle it in
  `runDaemonChild` (`internal/cli/daemon_run.go`), since the daemon re-execs
  *before* cobra parses.
