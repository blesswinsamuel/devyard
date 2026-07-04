# Architecture

How `local-compose` works internally. Start here before touching
`internal/supervisor`, `internal/daemon`, or `internal/control`.

## Big picture

```
local-compose up -d  ──►  Supervisor (setsid, backgrounded, owns children)
                           │  spawns each service in its own process group
                           │  serves a Unix-socket control protocol
                           ▼
            $XDG_RUNTIME_DIR/local-compose/<project>/supervisor.sock
            $XDG_RUNTIME_DIR/local-compose/<project>/supervisor.pid

local-compose ps / logs / restart / down / tui  ──►  control.Client (socket)
```

There is exactly one **Supervisor** process per project. It owns the child
processes and a **control server**. Everything else — `ps`, `logs`, `restart`,
`down`, and the TUI — is a thin client that dials the supervisor's Unix socket
and speaks the protocol in [control-protocol.md](control-protocol.md).

## The two ways to run a supervisor

- **Foreground `up`** (`internal/cli/up.go` -> `runSupervisor` in
  `supervisor_run.go`): the current process *is* the supervisor. It mirrors each
  service's output to the terminal with a colored prefix, installs a
  SIGINT/SIGTERM handler, and blocks until services exit. Foreground `up`
  returns non-zero if any service fails its `depends_on` conditions
  (`Supervisor.Failed()`), so CI catches misconfigured health gates.

- **Detached `up -d`** (`internal/cli/up.go` -> `upDaemon` ->
  `daemon.Spawn`): the CLI re-execs **the same binary** as a new session leader
  (`SysProcAttr{Setsid: true}`), with stdio repointed at
  `$XDG_STATE_HOME/local-compose/<project>/supervisor.log`, writes a pidfile,
  releases the child, and returns 0. The child is detected in `cli.Execute()`
  (in `internal/cli/root.go`) by the hidden `--supervisor <project>` flag and
  routed straight into the foreground supervisor code path, bypassing cobra.

  Go has no `fork(2)` binding, so we use the re-exec-then-`setsid` idiom
  instead of a double-fork. `setsid` + `cmd.Process.Release()` is sufficient on
  modern unices: the child is a session leader and survives parent exit.

`up -d` refuses to start if `daemon.Running` finds a live pidfile, so a project
can't be double-supervised. `down` is idempotent: if no supervisor is running it
just removes any stale pidfile/socket.

## Supervisor (`internal/supervisor`)

`Supervisor` owns a `map[string]*serviceRuntime` plus a topological `order`.
`Start` launches one `runService` goroutine per service (in order); `Wait`
blocks on a `sync.WaitGroup` until every run loop has exited.

Per-service run loop (`runService`):

1. Build the `health.Checker` up front (without probing) so
   `depends_on: service_healthy` waiters observe a `starting` checker, not nil.
2. `waitForDeps` — block until each `depends_on` condition is satisfied:
   - `service_started`: dependency's `startedOnce` flag is set.
   - `service_healthy`: dependency's checker reports `healthy` (fail fast on
     `unhealthy`, or if the dependency's run loop exits first).
   - Polls every `depPollInterval` (20ms); also selects on `ctx.Done()` and
     `stopCh` so it can't hang forever.
3. Launch loop: `launch` starts the command via `sh -c` (or the configured
   `shell`) with `Setpgid`, pipes stdout+stderr to the service logger, records
   pid/pgid, and sets `startedOnce`. Two pipe-reader goroutines drain the lines.
4. `cmd.Wait()`; record exit code, clear pid/pgid, set `status = exited`.
5. Apply restart policy (`restart.go`): `no` stops; `always`/`unless-stopped`
   retry indefinitely; `on-failure` retries up to `Backoff.MaxAttempts`. Backoff
   is exponential with jitter (`sleepBackoff`). `stopped`/stopping short-circuits.

`Stop` (used by `down`): marks every service stopped, writes "stopped" markers
for `unless-stopped` services, `SIGTERM`s every group, waits up to
`GracefulStopTimeout` (default 10s), then `SIGKILL`s survivors.

`StopService` / `Restart`: stop one service in place; `Restart` resets state and
re-launches a fresh run loop for that service. `Restart` removes any stopped
marker so the service resumes normally afterward.

Process groups are mandatory and non-negotiable: `launch` calls
`applyProcessGroup` (`proc_unix.go`) and teardown uses `killGroup(pgid, sig)`
with a **negative** pid to signal the whole group. This is what prevents
orphaned children when `command` is a shell pipeline.

## Healthchecks (`internal/health`)

State machine: `starting -> healthy | unhealthy`. A `Checker` runs the probe on
`Interval`, up to `Retries` consecutive failures before flipping to
`unhealthy`; one success recovers to `healthy` and resets the counter. Probes
run in their own process group so `CMD-SHELL` pipelines can be killed wholesale
after `Timeout`.

Probe forms (from `healthcheck.test`):
- `["CMD", "exe", "args..."]` — exec'd directly, no shell.
- `["CMD-SHELL", "command"]` — run via `Shell -c`.

The checker inherits the service's `shell`, `working_dir`, and `env` so a
`CMD-SHELL` probe runs in the same context as the service. Checkers are
frontend-agnostic: the supervisor exposes `chk.State()` via
`ServiceState.Health`, so CLI, TUI, and the future web UI all read the same
value.

Defaults (applied in `config.Validate`): `interval 5s`, `timeout 2s`,
`retries 3`.

## Control plane (`internal/control`)

`Server` listens on a Unix socket and serves each connection in its own
goroutine. It talks to the supervisor through the `Backend` interface
(`States`, `Stop`, `StopService`, `Restart`, `LogPath`) — the interface keeps
control decoupled from supervisor internals and lets tests drive the server
with a fake. A `Logs{follow:true}` request holds the connection open and
streams `log_line` frames as the file grows, ending with `done` on shutdown or
client disconnect.

`Client` is the matching thin client used by every CLI command and the TUI. One
client owns one connection and serves one request at a time — **not** safe for
concurrent use. The TUI opens separate connections for its periodic `List`
polls and its long-lived log-follow stream.

Wire format and message kinds are documented in
[control-protocol.md](control-protocol.md).

## State on disk (`internal/project`)

Per-project, XDG-compliant. Never hardcode these paths; always go through
`project.Resolve` / `Locations`.

| Path | Holds | Persisted? |
| --- | --- | --- |
| `$XDG_RUNTIME_DIR/local-compose/<project>/supervisor.sock` | control socket | no (cleared on reboot) |
| `$XDG_RUNTIME_DIR/local-compose/<project>/supervisor.pid` | supervisor pidfile | no |
| `$XDG_STATE_HOME/local-compose/<project>/supervisor.log` | daemon's own stdout/stderr | yes |
| `$XDG_STATE_HOME/local-compose/<project>/logs/<svc>.log` | per-service logs | yes |
| `$XDG_STATE_HOME/local-compose/<project>/.stopped-<svc>` | `unless-stopped` marker | yes |

Fallbacks: `$XDG_RUNTIME_DIR` -> `/tmp/local-compose`; `$XDG_STATE_HOME` ->
`~/.local/state`. Runtime dirs are `0o700` (they grant control over supervised
processes); state/log dirs are `0o755`.

## Concurrency model

- One goroutine per service (`runService`) for wait + restart.
- One `acceptLoop` goroutine in the control server, one handler goroutine per
  connection.
- One signal-handler goroutine (foreground + daemon) when
  `Options.InstallSignalHandler` is true (tests leave it off).
- One goroutine per health checker; one per pipe reader.
- Shared mutable state is a small locked struct (`serviceRuntime.mu` per
  service, `Supervisor.mu` for the map). Atomic flags (`started`, `stopped`,
  `failed`, `startedOnce`, `stopped`) guard the cross-goroutine booleans.
- Cancellation flows through `context.Context` and the supervisor's `stopCh`.

Avoid channels-for-everything; the locked-struct style is intentional and
easier to reason about for this map-of-services state.
