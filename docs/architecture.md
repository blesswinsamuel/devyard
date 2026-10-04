# Architecture

How devyard works inside. For the wire API see
[control-protocol.md](control-protocol.md); for `devyard.yml` see
[config-schema.md](config-schema.md).

## Big picture

```
 devyard CLI ──┐                       ┌── runner ── service process group
 web UI ───────┼─▶ daemon ─▶ engine ───┼── runner ── task process group
 (Watch, API)  │   (api, web,          └── runner ── terminal shell
               │    proxy, bus)
               └── unix socket / HTTP
```

- **One daemon per user** owns the project registry, the control socket, the
  web dashboard and the reverse proxy.
- **Every process runs under its own runner.** A runner is a
  `devyard --runner` process in its own session. Runners hold the child's
  stdio and PTY, write its logs and record its exit. The daemon can restart
  or crash without affecting any supervised process.
- **The engine is made of actors.** Every project, service and task is one
  goroutine that owns its state and processes commands in order. Concurrent
  API calls cannot race each other.
- **State is observed through `Watch`.** Clients get a snapshot followed by
  revisioned changes, so they never poll.

## Runner (`internal/runner`)

A runner supervises exactly one run and is spawned by the engine via
`runner.Launch`. It receives a `Spec` on stdin (command, env, dir, tty,
optional build step, paths) and:

1. Runs the optional build command in its own process group. A failed build
   ends the run.
2. Starts the child in its own process group. With `tty` it starts the child
   in its own session with a PTY (`Setsid`+`Setctty`; `Setpgid` must not be
   combined with `Setsid`).
3. Pumps output to the run's log (`internal/logstore`), to attached clients,
   and to a 256 KiB replay ring buffer.
4. Serves a control socket (`$XDG_RUNTIME_DIR/devyard/r/<hash>-<run>.sock`, unique per run) with
   these operations:
   - `status`, `watch` (phase changes) and `wait`
   - `stop`: SIGTERM, then SIGKILL after the grace period. It replies when
     the run has exited.
   - `signal`
   - `attach`: replay followed by live output, plus input, resize and EOF
5. When the child exits:
   - kills stragglers left in its process group,
   - drains output for a bounded time (an escaped grandchild holding the
     pipe cannot wedge it),
   - writes `status.json`,
   - delivers the exit to watchers and attached clients, then exits.

The runner is the only writer of `status.json`. When a runner dies without
recording an exit (reboot, SIGKILL), readers resolve the run as **lost** and
kill any orphaned group. Terminal sessions are ephemeral runs, which keep no
log files.

## Engine (`internal/engine`)

### Service actor

A service actor has one goroutine and one mailbox. Its commands are Start,
Stop, Restart, Kill, Update, Attach and Shutdown. Its events are phase, exit,
health, backoff and dependencies ready. The rules:

- **Stop replies once the process is gone.** Restart is "stop, then begin"
  inside the actor, so two run loops can never exist for one service.
- **Waits are events inside the actor.** Waiting for dependencies and
  backoff timers post events back to the mailbox, so a Stop interrupts them
  immediately.
- **Health checkers are per run.** A new `internal/health` checker is created
  when a run starts and stopped when it exits. Events are tagged with the run
  number, so stale results are ignored.
- **Restart policy:** `on-failure` and `always` back off exponentially with
  jitter (500ms up to 30s). The backoff resets after a run that lasted 10s.
  `on-failure` gives up after 10 consecutive quick failures.
- **Adoption is the normal path.** On creation the actor opens its process
  directory:
  - a live run is adopted;
  - a run that ended on its own while no daemon was watching applies the
    restart policy;
  - a run that was stopped or lost starts fresh if the service is wanted.
- **Config changes restart only what changed.** If the adopted run's
  definition hash differs from the current config, the actor restarts it.

### Task actor

Task actors work like service actors but run to completion. `Run` returns a
run number immediately. The run is owned by the daemon, not the caller, so a
disconnecting client never kills it. A second `Run` while one is in progress
fails with `ErrAlreadyRunning`.

### Project actor

The project actor serializes lifecycle commands: start (all or selected),
stop, restart, reload, remove, and start of a single service. It persists the
registration (`project.json`) and publishes an immutable `View` (config,
definitions, actor handles) that readers use without locks.

- **Stop** marks the project stopped (it won't autostart) and stops tasks,
  then services in reverse dependency order, one level at a time.
- **Reload** diffs the new config against the running one:
  - added services start,
  - removed ones stop,
  - changed ones restart,
  - everything else is untouched.

  A config that fails to load puts the project in the `error` status but
  never stops or deregisters it.
- **Project status** is derived from service states:
  `stopped | starting | running | degraded | stopping | error`.

Per-service commands that must stay responsive (stop, restart, kill,
attach) go directly to the service actor through the `View`, so a kill is
never queued behind a slow project stop.

### Manager

The manager is the registry of project actors. `Add` either registers a
config or re-registers an existing one, so concurrent `devyard start` calls
funnel into the same actor and never duplicate processes. Project ids are
validated slugs. A different config declaring the same name fails with
`ErrAlreadyExists`.

## Logs (`internal/logstore`)

Each process has `procs/<kind>-<name>/runs/<run>.log`. Records look like
this:

```
<seq> <unix-nanos> <o|e|s> <text>
```

`seq` increases within a run, including across size rotation (to
`<run>.old.log`). A run never overwrites another run, so "previous" always
means the previous run. The last 10 runs are kept.

A `Follower` drains a rotated segment before reopening, and moves to a newer
run only after the old one is fully read, so no line is lost at a rotation or
restart.

## State bus (`internal/events`)

The bus is a materialized view of every entity (projects, services, tasks,
git status, daemon info) plus a revision counter.

- `Subscribe` atomically returns a snapshot and registers the subscriber.
- Publishing never blocks. A subscriber that overflows its buffer receives a
  fresh snapshot instead of a gap.
- Unchanged upserts are dropped.

The engine publishes through an `Observer`, which is implemented by the
daemon's presenter. The presenter also computes proxy URLs.

## Daemon (`internal/daemon`)

- **Startup:**
  1. Take the flock and write the pidfile (only the lock holder writes it).
  2. Bind the control socket, web and proxy listeners, so collisions fail
     fast and ports are known.
  3. Load projects, which adopts and autostarts them.
  4. Serve.
- **Control socket:** unencrypted HTTP/2, so the CLI can use bidirectional
  `Attach`.
- **Exit modes:**
  - SIGTERM/SIGINT: leave services running.
  - `StopDaemon`: stop services.
  - `RestartDaemon`: drain, release every listener and the lock, then spawn
    the replacement, so two daemons never overlap.

## Web (`internal/web`)

The dashboard serves the embedded SPA, the ConnectRPC API, and
`/ws/attach` (one websocket per interactive session).

- **Host allowlist:** every request needs an allowed Host header: loopback
  names, IP literals, `devyard`, anything under the proxy domain suffix, or
  `web.allowed_hosts`. This blocks DNS rebinding.
- **Origin check:** websockets require the same Origin as the Host.
- **Password (opt-in):** when `web.password_hash` is set
  (`devyard auth set-password`), the API and `/ws/attach` require a login
  cookie (bcrypt check via `POST /auth/login`; HMAC-signed expiry, no server
  state, so sessions die with the daemon). The SPA itself is served either
  way and renders a login screen. Unauthenticated API calls get a
  Connect-protocol `unauthenticated` error body.

## Sessions (`internal/sessions`)

Sessions open terminals (runner-backed shells in the project directory) and
attachments to running tasks and TTY services.

- **Terminals** are identified by a session id. They survive websocket drops,
  page reloads and daemon restarts. Reattaching replays recent output.
- **Tasks and services** are attached through their actors.

## Git (`internal/gitstate`, `internal/gitlog`, `internal/gitwatcher`)

- The watcher reference-counts watched paths per project, so projects that
  share a repository don't steal each other's events. It also watches new
  branch namespace directories and worktree common dirs.
- Status is published on the bus with a `change_seq` that clients use to
  invalidate cached logs and diffs.
- Remote operations are serialized per project and bounded by a timeout. They
  never prompt for credentials and run in their own process group.

## Reverse proxy (`internal/proxy`)

Routes are resolved per request from the project views and the live service
status. `<svc>.<project>.<suffix>` goes to `127.0.0.1:<port>`. Services that
are not running get a 503 page. TLS works with custom certs, the mkcert CA,
or devyard's own local CA.

## State on disk (`internal/paths`)

```
$XDG_STATE_HOME/devyard/
  daemon.log
  projects/<id>/project.json            registration, captured env (0600), desired state
  projects/<id>/procs/<kind>-<name>/    status.json, runner.log, runs/*.log
  terminals/<session>/                  live terminal runs
  ca/                                   local TLS CA (when used)
$XDG_RUNTIME_DIR/devyard/               (falls back to $XDG_STATE_HOME/devyard/run)
  daemon.sock daemon.pid daemon.lock r/<hash>.sock
$XDG_CONFIG_HOME/devyard/config.yml     global config
```

## Concurrency rules

- **Single owner.** State is owned by exactly one actor goroutine; other
  goroutines send messages.
- **Immutable reads.** Readers use immutable snapshots (`View`, bus
  entities), never shared maps.
- **Observer calls** for one entity happen from its actor in order and must
  not block or call back into the engine.
- **Process groups.** Every child runs in its own process group or session.
  The daemon never signals raw pids; it goes through the runner.
