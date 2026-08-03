# Architecture

How `local-compose` works internally. Start here before touching
`internal/orchestrator`, `internal/supervisor`, `internal/daemon`, or
`internal/control`.

## Big picture

```
local-compose up  ──►  ensureDaemon() ──►  global Daemon (setsid, backgrounded)
                                            │  owns map[string]*supervisor.Supervisor
                                            │  serves one Unix-socket control protocol
                                            │  optionally serves web UI (WS + SPA)
                                            ▼
                          $XDG_RUNTIME_DIR/local-compose/daemon.sock

local-compose ps / logs / restart / down / tui / web  ──►  control.Client (socket)
```

There is exactly one **global daemon** process. It owns one
`*supervisor.Supervisor` per project and serves a single **Unix-socket control
protocol** at `$XDG_RUNTIME_DIR/local-compose/daemon.sock`. Every CLI command
(`ps`, `logs`, `restart`, `down`), the TUI, and the web UI are thin **Client**
connections over that socket. The daemon autostarts projects whose services
declare `restart: always` or `restart: unless-stopped` on startup. See
[control-protocol.md](control-protocol.md) for the wire format.

## The global daemon

The daemon is a single process (`local-compose --daemon`, spawned by `up` or
`start-daemon`) that owns an `orchestrator.Daemon` (`internal/orchestrator`)
which in turn owns a `map[string]*supervisor.Supervisor`. The daemon child is
created via `setsid` re-exec (`internal/daemon`): the CLI re-execs **the same
binary** as a new session leader (`SysProcAttr{Setsid: true}`), with stdio
repointed at `$XDG_STATE_HOME/local-compose/daemon.log`, writes a pidfile at
`$XDG_RUNTIME_DIR/local-compose/daemon.pid`, and releases the child.

The daemon child is detected in `cli.Execute()` (`internal/cli/root.go`)
*before* cobra runs, via the hidden `--daemon` flag (`daemon.DaemonFlag`). It
runs `runDaemonChild` (`internal/cli/daemon_run.go`), not a cobra command.
`runDaemonChild` creates the orchestrator, starts the control server, runs
`Autostart()`, optionally starts the web UI (if `web.enabled` in global config),
and blocks on a signal or `StopDaemon`.

Go has no `fork(2)` binding, so we use the re-exec-then-`setsid` idiom instead
of a double-fork. `setsid` + `cmd.Process.Release()` is sufficient on modern
unices: the child is a session leader and survives parent exit.

`start-daemon` refuses to start if `daemon.DaemonRunning` finds a live pidfile.
`stop-daemon` sends `stop_daemon` over the socket, waits for the process to
exit, and removes the pidfile.

## Orchestrator (`internal/orchestrator`)

The `Daemon` struct owns a `map[string]*Project` (project name → `*Project`).
Each `Project` holds a `*supervisor.Supervisor`, its config path, a cancel
function, and a done channel.

Key methods:

- **`StartProject(configPath, build)`** — loads the config, creates a
  `Supervisor`, starts it, and adds it to the map. Writes a `config-path` file
  in the project's state dir (for autostart discovery). Removes any
  project-level `.stopped` marker.
- **`StopProject(name)`** — stops the supervisor, writes a project-level
  `.stopped` marker, closes the supervisor, and removes the project from the
  map.
- **`StopDaemon()`** — stops all projects and signals the daemon process to
  exit (closes `StopCh`).
- **`ListProjects()`** — returns a snapshot of all known projects and their
  statuses.
- **`ProjectBackend(project)`** — returns the `control.Backend` for the named
  project (used by the control server to route per-project requests).
- **`Autostart()`** — scans `$XDG_STATE_HOME/local-compose/*/` for
  `config-path` files, reads each project's config, and starts projects whose
  services have `restart: always` or `restart: unless-stopped` (unless a
  project-level `.stopped` marker exists).

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

`Stop` (used by `down`/`StopProject`): marks every service stopped, writes "stopped" markers
for `unless-stopped` services, `SIGTERM`s every group, waits up to
`GracefulStopTimeout` (default 10s), then `SIGKILL`s survivors.

`StopService` / `Restart`: stop one service in place; `Restart` resets state and
re-launches a fresh run loop for that service. `Restart` removes any stopped
marker so the service resumes normally afterward.

On the next `up`, any `unless-stopped` service with a persisted marker is
skipped at `Start` (status `stopped`, `startedOnce` stays false). `waitForDep`
distinguishes a skipped dependency (`errDependencyStopped`) from one that
genuinely exited: skipped dependents are skipped transitively and `Failed()`
stays false, so `up` exits 0. A dependency that exits or goes unhealthy before
satisfying its condition is a real failure (`Failed()` -> non-zero `up`). A
user-initiated shutdown during startup (`errSupervisorStopping`) is also not a
failure.

Process groups are mandatory and non-negotiable: `launch` calls
`applyProcessGroup` (`proc_unix.go`) and teardown uses `killGroup(pgid, sig)`
with a **negative** pid to signal the whole group. This is what prevents
orphaned children when `command` is a shell pipeline.

`Top` (the `local-compose top [service]` command) aggregates the resource usage
of each service's process group. It samples every group twice around one shared
1s interval (`internal/procstat.SampleGroup`, procfs on Linux / libproc on
macOS) and reports per-service CPU% (delta over the interval) and aggregate
RSS. Because the group leader is the service's shell, this captures the whole
`sh -c` tree, not just the supervisor's direct child.

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
`ServiceState.Health`, so CLI, TUI, and web UI all read the same value.

Defaults (applied in `config.Validate`): `interval 5s`, `timeout 2s`,
`retries 3`.

## Control plane (`internal/control`)

`Server` listens on a Unix socket and serves each connection in its own
goroutine. It talks to the daemon through the `MultiBackend` interface
(`ListProjects`, `StartProject`, `StopProject`, `StopDaemon`,
`ProjectBackend`) which routes per-project requests to the right
`Backend` (`States`, `Stop`, `StopService`, `Restart`, `LogPath`). The
interface keeps control decoupled from supervisor/orchestrator internals and
lets tests drive the server with a fake. A `Logs{follow:true}` request holds
the connection open and streams `log_line` frames as the file grows, ending
with `done` on shutdown or client disconnect.

`Client` is the matching thin client used by every CLI command, the TUI, and
the web UI. One client owns one connection and serves one request at a time —
**not** safe for concurrent use. The TUI opens separate connections for its
periodic `List` polls and its long-lived log-follow stream.

Wire format and message kinds are documented in
[control-protocol.md](control-protocol.md).

## Web UI (`internal/web`)

The web UI is a WebSocket frontend over the same control socket. The daemon
starts it if `web.enabled` is true in the global config
(`$XDG_CONFIG_HOME/local-compose/config.yml`). It serves an embedded SolidJS
SPA (built with bun + Vite, using xterm.js for log rendering) from
`internal/web/dist/` via `go:embed`. The WS endpoint dispatches JSON messages
to the same `MultiBackend` interface used by the control server.

Default bind address is `127.0.0.1:9090` (loopback only). Users who want
remote access must explicitly set `web.host: 0.0.0.0` in the global config.

## State on disk (`internal/project`)

Per-project and daemon-level, XDG-compliant. Never hardcode these paths;
always go through `project.Resolve` / `Locations` (per-project) or
`project.ResolveDaemon` / `DaemonLocations` (daemon-level).

### Daemon-level paths

| Path | Holds | Persisted? |
| --- | --- | --- |
| `$XDG_RUNTIME_DIR/local-compose/daemon.sock` | control socket | no (cleared on reboot) |
| `$XDG_RUNTIME_DIR/local-compose/daemon.pid` | daemon pidfile | no |
| `$XDG_STATE_HOME/local-compose/daemon.log` | daemon's own stdout/stderr | yes |

### Per-project paths

| Path | Holds | Persisted? |
| --- | --- | --- |
| `$XDG_STATE_HOME/local-compose/<project>/config-path` | config file path (for autostart discovery) | yes |
| `$XDG_STATE_HOME/local-compose/<project>/.stopped` | project-level stopped marker | yes |
| `$XDG_STATE_HOME/local-compose/<project>/logs/<svc>.log` | per-service logs | yes |
| `$XDG_STATE_HOME/local-compose/<project>/<svc>.stopped` | per-service `unless-stopped` marker | yes |

Fallbacks: `$XDG_RUNTIME_DIR` -> `/tmp/local-compose`; `$XDG_STATE_HOME` ->
`~/.local/state`. Runtime dirs are `0o700` (they grant control over supervised
processes); state/log dirs are `0o755`.

## Global config (`internal/globalconfig`)

The global config lives at `$XDG_CONFIG_HOME/local-compose/config.yml` (default
`~/.config/local-compose/config.yml`). It holds settings that apply across all
projects:

```yaml
web:
  enabled: true
  host: 127.0.0.1
  port: 9090
```

The daemon loads it on startup. Unknown fields produce a warning but don't
error.

## Concurrency model

- One goroutine per service (`runService`) for wait + restart.
- One `acceptLoop` goroutine in the control server, one handler goroutine per
  connection.
- One signal-handler goroutine in the daemon child.
- One goroutine per health checker; one per pipe reader.
- One goroutine per WS connection in the web server.
- Shared mutable state is a small locked struct (`serviceRuntime.mu` per
  service, `Supervisor.mu` for the map, `Daemon.mu` for the projects map).
  Atomic flags (`started`, `stopped`, `failed`, `startedOnce`, `stopped`)
  guard the cross-goroutine booleans.
- Cancellation flows through `context.Context` and the supervisor's `stopCh`.

Avoid channels-for-everything; the locked-struct style is intentional and
easier to reason about for this map-of-services state.
