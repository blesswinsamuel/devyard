# Adding a CLI command

A recipe for adding a new `devyard <command>` end to end, following how
the existing commands are wired. Use this as a checklist.

## Decide what kind of command it is

- **Control command** (talks to a running daemon): `ps`, `logs`, `restart`,
  `stop` — these dial the daemon's control socket and send a request. You'll
  need a new protocol kind; see [control-protocol.md](control-protocol.md) >
  "Adding a new request kind".
- **Local command** (no daemon needed): `build` — runs against the config
  directly. No protocol change.
- **Frontend command**: the web UI — a long-running client of the
  control socket.
- **Daemon-management command**: `start-daemon`, `stop-daemon` — manage the
  global daemon process itself.

## Steps (control command example: `devyard pause <service>`)

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

Add a command constructor factory to the appropriate resource file (e.g. `cmd_service.go`):

```go
func newServicePauseCmd(ctx *CLIContext) *cobra.Command {
    return &cobra.Command{
        Use:   "pause [service]",
        Short: "Pause one or all services",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            cfg, err := ctx.LoadConfig()
            if err != nil {
                return err
            }
            service := ""
            if len(args) == 1 {
                service = args[0]
            }
            client, err := ctx.DialDaemon()
            if err != nil {
                ctx.Errorln("devyard: no daemon running")
                return err
            }
            defer func() { _ = client.Close() }()
            if err := client.Pause(cfg.Project, service); err != nil {
                return err
            }
            ctx.Errorf("devyard: paused %q\n", service)
            return nil
        },
    }
}
```

Register it on the parent resource command (and optionally as a top-level shortcut in `internal/cli/root.go`):

```go
serviceCmd.AddCommand(newServicePauseCmd(ctx))
```

### 6. Tests + docs

- Unit test in `internal/control/control_test.go` (fake backend) and
  `internal/supervisor/` (real behavior).
- CLI unit test in `internal/cli/cli_test.go` to test command tree and argument parsing.
- Add an integration case in `test/integration/e2e_test.go` if the command is
  user-facing and observable through the binary.
- Add a row to the Commands table in [README.md](../README.md).
- Add a row to the request-kind table in
  [docs/control-protocol.md](control-protocol.md).

## Patterns to copy

| You're adding... | Copy |
| --- | --- |
| A project command | `internal/cli/cmd_project.go` |
| A service command (`restart`, `stop`, `kill`) | `internal/cli/cmd_service.go` |
| A task command (`run`, `logs`) | `internal/cli/cmd_task.go` |
| A streaming command (`logs`) | `internal/cli/proc_helpers.go` + `client.Logs` |
| A local command (`build`) | `internal/cli/build.go` (no socket) |
| A daemon-management command | `internal/cli/daemon_cmd.go` |

## Key helpers on CLIContext

| Helper | What it does |
| --- | --- |
| `ctx.LoadConfig()` | Finds, parses, validates the config, derives project name, computes topo order. |
| `ctx.ResolveProjectName()` | Resolves project name from config file or `-p` flag without full validation. |
| `ctx.EnsureDaemon()` | Checks if the daemon is running, spawns it if not, returns control client. |
| `ctx.DialDaemon()` | Dials the daemon's control socket. Returns an error if the daemon is not running. |
| `ctx.Errorf(format, ...)` | Writes formatted message to `ctx.Err` without unchecked error returns. |
| `ctx.PrintJSON(val)` | Encodes structured output when `-o json` is set. |

## Gotchas

- **`-f` is used for `--follow`** on logs commands (`logs`, `svc logs`, `task logs`). `--file` is available persistently across commands without a shorthand.
- **Use `CLIContext` streams** (`ctx.Out`, `ctx.Err`) rather than `os.Stdout`/`os.Stderr` so commands remain isolated and unit-testable.
- **Close the client** (`defer client.Close()`).
- **`ctx.LoadConfig()`** already finds the config (walking up from cwd), validates it,
  derives the project name, and computes the topological `Order` — reuse it,
  don't re-derive.
- If the daemon child needs to inherit a new flag, handle it in
  `runDaemonChild` (`internal/cli/daemon_run.go`), since the daemon re-execs
  *before* cobra parses.
