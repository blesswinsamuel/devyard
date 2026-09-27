# Architecture

How `devyard` works internally. Start here before touching
`internal/orchestrator`, `internal/supervisor`, `internal/daemon`, or
`internal/control`.

## Big picture

```
devyard start ──►  ensureDaemon() ──►  global Daemon (setsid, backgrounded)
                                            │  owns map[string]*supervisor.Supervisor
                                            │  serves one Unix-socket control protocol
                                            │  optionally serves web UI (WS + SPA)
                                            ▼
                          $XDG_RUNTIME_DIR/devyard/daemon.sock

devyard ps / logs / restart / stop / web  ──►  control.Client (socket)
```

There is exactly one **global daemon** process. It owns one
`*supervisor.Supervisor` per project and serves a single **Unix-socket control
protocol** at `$XDG_RUNTIME_DIR/devyard/daemon.sock`. Every CLI command
(`ps`, `logs`, `restart`, `stop`) and the web UI are thin **Client**
connections over that socket. The daemon autostarts every registered project
on startup unless it carries a project-level `.stopped` marker (written by
`stop`). See
[control-protocol.md](control-protocol.md) for the wire format.

## The global daemon

The daemon is a single process (`devyard --daemon`, spawned by `start` or
`daemon start`) that owns an `orchestrator.Daemon` (`internal/orchestrator`)
which in turn owns a `map[string]*supervisor.Supervisor`. The daemon child is
created via `setsid` re-exec (`internal/daemon`): the CLI re-execs **the same
binary** as a new session leader (`SysProcAttr{Setsid: true}`), with stdio
repointed at `$XDG_STATE_HOME/devyard/daemon.log`, writes a pidfile at
`$XDG_RUNTIME_DIR/devyard/daemon.pid`, and releases the child.

The daemon child is detected in `cli.Execute()` (`internal/cli/root.go`)
*before* cobra runs, via the hidden `--daemon` flag (`daemon.DaemonFlag`). It
runs `runDaemonChild` (`internal/cli/daemon_run.go`), not a cobra command.
`runDaemonChild` creates the orchestrator, starts the control server, starts
the web dashboard server and reverse proxy, runs `Autostart()`, and blocks on
a signal or `StopDaemon`.

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
  in the project's state dir (for autostart discovery). Removes the
  project-level `.stopped` marker so
  explicit `start` resumes previously stopped projects. If the project is
  already running, resumes stopped/exited services without recreating the
  supervisor. If a stop is in progress, waits for it then recreates.
- **`StartService(project, service)`** — lazily starts a single service on a
  stopped project: materializes a supervisor limited to that service plus its
  transitive `depends_on` chain (unselected services are registered as stopped
  and skipped), and clears the project-level `.stopped` marker.
- **`StopProject(name)`** — stops the supervisor, writes a project-level
  `.stopped` marker, closes the supervisor (retained for `ps` state queries),
  and **keeps the project in the map** (`status: stopped`) so list commands
  still show it.
- **`StopDaemon()`** — stops all projects (nils each `Sup`, keeps map entries)
  and signals the daemon process to exit (closes `StopCh`). Does not write
  project `.stopped` markers.
- **`ListProjects()`** — returns a snapshot of all known projects (running and
  stopped) and their statuses.
- **`ProjectBackend(project)`** — returns the `control.Backend` for the named
  project. A stopped project's closed supervisor is still returned so `ps`
  works; mutating calls error because the supervisor is stopped.
- **`Autostart()`** — scans `$XDG_STATE_HOME/devyard/*/` for
  `config-path` files, reads each project's config, and starts every project
  **unless** a project-level `.stopped` marker exists. Skipped
  projects are still registered in the map as stopped so list commands stay
  complete.

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
5. Apply restart policy (`restart.go`): `no` stops; `always`
   retries indefinitely; `on-failure` retries up to `Backoff.MaxAttempts`. Backoff
   is exponential with jitter (`sleepBackoff`). `stopped`/stopping short-circuits.

`Stop` (used by `stop`/`StopProject`): marks every service stopped,
`SIGTERM`s every group, waits up to
`GracefulStopTimeout` (default 10s), then `SIGKILL`s survivors.

`StopService` / `Restart`: stop one service in place; `Restart` resets state and
re-launches a fresh run loop for that service. `StartService` resumes a stopped
service in place (no-op when it is already running).

A supervisor can be materialized with a `Selected` set of service names (from
`start <svc>` on a stopped project): services outside the set are registered as
`stopped` and skipped at `Start` (`startedOnce` stays false), so only the
selected service and its `depends_on` chain launch. Skips are logged
(`skipping service <name>`) and are not failures — `Failed()` stays false.
Only a dependency that genuinely exits or goes unhealthy before satisfying its
condition sets `Failed()` (non-zero exit); a user-initiated shutdown during
startup (`errSupervisorStopping`) is not a failure.

Process groups are mandatory and non-negotiable: `launch` calls
`applyProcessGroup` (`proc_unix.go`) and teardown uses `killGroup(pgid, sig)`
with a **negative** pid to signal the whole group. This is what prevents
orphaned children when `command` is a shell pipeline.

`Top` (the `devyard top [service]` command) aggregates the resource usage
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
`ServiceState.Health`, so CLI and web UI both read the same value.

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

`Client` is the matching thin client used by every CLI command and the web
UI. One client owns one connection and serves one request at a time — **not**
safe for concurrent use. The web bridge dials a short-lived connection per
request and a dedicated long-lived connection per log-follow stream or event
subscription.

Wire format and message kinds are documented in
[control-protocol.md](control-protocol.md).

## Web UI (`internal/web`)

The web UI is served directly by the daemon on its configured host and port
(default `127.0.0.1:9090`), as well as via the reverse proxy (`localhost:8080`
and `devyard.<suffix>:8080`). It serves an embedded SolidJS SPA (built with bun + Vite,
using xterm.js for log rendering) from `internal/web/dist/` via `go:embed`.
The ConnectRPC handler is mounted directly in-process, and interactive terminal
sessions are managed over `/ws` via `creack/pty`.

Every message sent to a browser goes through a per-connection outbound queue
drained by a single writer goroutine: enqueueing never blocks, so a dead or
slow tab can't stall supervision callbacks or other viewers. A ping/pong loop
reaps half-open connections; terminals are torn down with their whole process
group when the owning browser disconnects.

Default bind address is `127.0.0.1:9090` (loopback only). Override with
`--host` / `--port`, or set defaults in the global config (`web.host`,
`web.port`). Users who want remote access must explicitly bind to `0.0.0.0`.

## Reverse proxy (`internal/proxy`)

The daemon child starts an HTTP/HTTPS reverse proxy (default `127.0.0.1:8080`,
settings from the global config's `proxy` section) so services that declare
`port`/`ports` are reachable at named URLs like
`http://<service>.<project>.localhost` or `https://<service>.<project>.localhost:8443`.
Routes are resolved live on every request: `orchestrator.Daemon.ProxyRoutes()`
(implements `proxy.Resolver`) walks the project map, reads each project's
retained `config.File`, and attaches the live supervisor status. Matching is an
exact, case-insensitive `Host`-header match over
`[<port>.]<label>.<project>.<domain_suffix>`; the first route wins on duplicates
(projects are iterated in sorted order). A service is forwarded to only while
`running`; otherwise the proxy serves a styled 503 page naming the service, and
an unreachable upstream gets a 502 page. The proxy preserves the original `Host`
header and passes websocket upgrades through (`httputil.ReverseProxy`,
`FlushInterval: -1`). A failed listen (port in use) disables the proxy but not
the daemon.

### TLS & Certificate Management (`internal/proxy/cert.go`)

When `proxy.tls.enabled` is true, the proxy opens an HTTPS listener on
`proxy.tls.port` (default `8443`, or `443` if `proxy.port` is `80`) alongside
the HTTP listener on `proxy.port`. If `proxy.tls.http_redirect` is true,
incoming HTTP requests receive a 307 Temporary Redirect to the corresponding
HTTPS URL.

Certificates are resolved dynamically per-connection:
1. **Custom certificates**: if `proxy.tls.cert_file` and `proxy.tls.key_file`
   are configured, they are loaded and served.
2. **mkcert integration**: if mkcert is installed and its root CA is found
   (via `CAROOT` or standard OS locations), devyard dynamically signs leaf
   certificates with mkcert's CA, making all proxied domains trusted by the
   user's browsers automatically with zero setup.
3. **Built-in Local CA**: otherwise, devyard generates an internal Root CA
   (`$XDG_STATE_HOME/devyard/ca/rootCA.pem`) and mints leaf certificates
   on-demand for requested hostnames.

URL display in `ps` and the Web UI reflects the active scheme (`https://` when
TLS is enabled) and port (see config-schema.md > Global config).

## State on disk (`internal/project`)

Per-project and daemon-level, XDG-compliant. Never hardcode these paths;
always go through `project.Resolve` / `Locations` (per-project) or
`project.ResolveDaemon` / `DaemonLocations` (daemon-level).

### Daemon-level paths

| Path | Holds | Persisted? |
| --- | --- | --- |
| `$XDG_RUNTIME_DIR/devyard/daemon.sock` | control socket | no (cleared on reboot) |
| `$XDG_RUNTIME_DIR/devyard/daemon.pid` | daemon pidfile | no |
| `$XDG_STATE_HOME/devyard/daemon.log` | daemon's own stdout/stderr | yes |

### Per-project paths

| Path | Holds | Persisted? |
| --- | --- | --- |
| `$XDG_STATE_HOME/devyard/<project>/config-path` | config file path (for autostart discovery) | yes |
| `$XDG_STATE_HOME/devyard/<project>/.stopped` | project-level stopped marker (suppresses daemon autostart) | yes |
| `$XDG_STATE_HOME/devyard/<project>/logs/<svc>.log` | per-service logs | yes |

Fallbacks: `$XDG_RUNTIME_DIR` -> `~/.local/state/devyard/run` (or per-project `<project>/run`);
`$XDG_STATE_HOME` -> `~/.local/state`. Runtime dirs are `0o700` (they grant control
over supervised processes); state/log dirs are `0o755`.

## Global config (`internal/globalconfig`)

The global config lives at `$XDG_CONFIG_HOME/devyard/config.yml` (default
`~/.config/devyard/config.yml`). It holds settings that apply across all
projects:

```yaml
web:
  host: 127.0.0.1
  port: 9090
```

These are default bind settings for the daemon's web dashboard. Unknown fields
produce a warning but don't error.

## Concurrency model

- One goroutine per service (`runService`) for wait + restart.
- One `acceptLoop` goroutine in the control server, one handler goroutine per
  connection.
- One signal-handler goroutine in the daemon child.
- One goroutine per health checker; one per pipe reader.
- One goroutine per WS connection in the web server (reader) plus one writer
  goroutine per connected browser draining its outbound queue; one goroutine
  per log subscription and per running action; one persistent daemon event
  pump with reconnect backoff.
- Shared mutable state is a small locked struct (`serviceRuntime.mu` per
  service, `Supervisor.mu` for the map, `Daemon.mu` for the projects map).
  Atomic flags (`started`, `stopped`, `failed`, `startedOnce`, `stopped`)
  guard the cross-goroutine booleans.
- Cancellation flows through `context.Context` and the supervisor's `stopCh`.

Avoid channels-for-everything; the locked-struct style is intentional and
easier to reason about for this map-of-services state.
