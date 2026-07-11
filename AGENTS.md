# AGENTS.md

Guidance for AI coding agents (and human contributors) working in this repo.
Read this before making changes.

## What this project is

`local-compose` is a docker-compose-style CLI that orchestrates **local processes**
(no Docker). macOS + Linux only. Written in Go 1.26. Module:
`github.com/blesswinsamuel/local-compose`. See [README.md](README.md) for the user
pitch and [docs/architecture.md](docs/architecture.md) for how it works inside.

## Essential commands

```bash
go build ./...                      # build everything
go build -o local-compose ./cmd/local-compose   # build the binary
go vet ./...                        # vet
gofmt -l .                          # must print nothing
go test ./...                       # all unit + integration tests
go test -race ./...                 # with the race detector (CI uses this)
go test ./internal/supervisor/...   # one package
go test ./test/integration/...      # the black-box e2e suite (builds the real binary)
golangci-lint run                   # lint; config in .golangci-lint.yml (v2)
```

Tests and lint must stay green. CI (`.github/workflows/ci.yml`) runs `go vet`,
`gofmt`, `golangci-lint`, `go build`, and `go test -race` on Ubuntu and macOS.
The integration suite builds the actual binary and drives the full CLI lifecycle
(`up -d`, `ps`, `logs`, `restart`, `down`, `build`, `up --build`,
`start-daemon`, `stop-daemon`) against isolated XDG dirs — it never touches the
user's real state.

## Repo layout

```
cmd/local-compose/main.go   # entrypoint -> cli.Execute()
internal/
  cli/         # cobra commands + flag wiring + the daemon re-exec handler (root.go)
  config/      # local-compose.yml schema, parsing, validation, defaults
  project/     # project name + XDG runtime/state dir resolution (per-project + daemon)
  dag/         # depends_on graph, cycle detection, topo order
  supervisor/  # owns child processes: launch, log capture, restart policy, health, stop
  daemon/      # setsid re-exec for the global daemon, pidfile, liveness checks (build tag: unix)
  orchestrator/ # multi-project manager: owns map[string]*supervisor.Supervisor, autostart
  control/     # Unix-socket server (MultiBackend interface) + thin Client
  protocol/    # wire frames: Request/Response, length-prefixed JSON
  health/      # per-service healthcheck state machine (starting -> healthy | unhealthy)
  logs/        # doc-only placeholder; actual logging lives in supervisor/logs.go
  tui/         # Bubble Tea v2 frontend (charm.land/bubbletea/v2)
  ui/          # shared status color/label helpers used by cli, tui, web
  web/         # WS server + embedded SolidJS SPA (xterm.js logs)
  globalconfig/ # user-level config ($XDG_CONFIG_HOME/local-compose/config.yml)
test/integration/  # black-box e2e tests
```

## Architecture in one paragraph

A single **global daemon** process (`local-compose --daemon`, spawned by `up`
or `start-daemon`) owns a `map[string]*supervisor.Supervisor` — one supervisor
per project. It serves one **Unix-socket control protocol** at
`$XDG_RUNTIME_DIR/local-compose/daemon.sock`. Every CLI command (`ps`, `logs`,
`restart`, `down`), the TUI, and the web UI are thin **Client** connections over
that socket. The daemon autostarts projects whose services declare
`restart: always` or `restart: unless-stopped` on startup. See
[docs/architecture.md](docs/architecture.md) and
[docs/control-protocol.md](docs/control-protocol.md).

## Conventions

- **Commit style**: conventional commits — `feat(tui): ...`, `fix(supervisor): ...`,
  `test(config): ...`, `docs: ...`. Keep commits focused and build-clean.
- **Go style**: `gofmt`'d, errors wrapped with `%w` and a leading package context
  (`fmt.Errorf("supervisor: ...: %w", err)`), `context.Context` for cancellation,
  no `log.Fatal` in packages (only `main`/CLI may exit).
- **No inline imports** beyond the top-of-file block.
- **Process safety**: every child is started with `Setpgid`; teardown uses
  `killpg` (negative pid) so shell pipelines don't leak orphans. Don't add a
  code path that spawns a child without its own process group.
- **Config changes**: update `internal/config/config.go` **and** the tests in
  `internal/config/config_test.go` **and** [docs/config-schema.md](docs/config-schema.md).
  Unknown fields should warn, not error.
- **Global config changes**: update `internal/globalconfig/globalconfig.go` and
  its tests. The global config lives at
  `$XDG_CONFIG_HOME/local-compose/config.yml`.
- **Protocol changes**: update `internal/protocol/protocol.go`, both sides in
  `internal/control`, and [docs/control-protocol.md](docs/control-protocol.md).
  The protocol is shared by the CLI, TUI, and web UI — keep messages
  frontend-agnostic (no terminal-specific fields).
- **Tests**: add unit tests in the package you changed. For cross-cutting
  behavior, prefer a black-box case in `test/integration/`.

## Gotchas

- **`-f` is the persistent config flag**, so `logs --follow` has no `-f` shorthand
  (it's `--follow`). Don't "fix" this.
- **Daemon re-exec** is detected in `cli.Execute()` *before* cobra runs, via the
  hidden `--daemon` flag (`daemon.DaemonFlag`). The daemon child runs
  `runDaemonChild` (`internal/cli/daemon_run.go`), not a cobra command. If you
  add root-level flags that the daemon child must inherit, handle them in
  `runDaemonChild`, not just in cobra.
- **`up` auto-starts the daemon**: `up` calls `ensureDaemon()` which spawns
  the daemon if it's not running, then sends `start_project` over the socket.
  Foreground `up` then follows logs from all services via `followForeground`.
  `up -d` just sends `start_project` and returns.
- **Autostart**: on daemon startup, `Autostart()` scans
  `$XDG_STATE_HOME/local-compose/*/` for `config-path` files, reads each
  project's config, and starts projects whose services have `restart: always`
  or `restart: unless-stopped` (unless a project-level `.stopped` marker
  exists). `on-failure` does not trigger autostart.
- **Project-level vs service-level stopped markers**: `StopProject` writes a
  project-level `$XDG_STATE_HOME/local-compose/<project>/.stopped` marker (so
  autostart won't resume the project). `StartProject` removes it. This is
  distinct from the per-service `<svc>.stopped` markers used by the
  supervisor's `unless-stopped` restart policy.
- **`unless-stopped`** (service-level) persists a per-service "stopped" marker
  in the state dir so a service doesn't auto-resume on the next `up`. `Restart`
  removes it. If you touch restart/stop logic, keep the marker in sync. When a
  service is skipped because of the marker, its dependents are skipped
  transitively (`waitForDep` returns `errDependencyStopped`) and `up` exits 0 —
  an explicit stop is not a failure, so `Failed()` stays false. Only a
  dependency that genuinely exits or goes unhealthy sets `Failed()`; a
  user-initiated shutdown (`errSupervisorStopping`) doesn't either.
- **Health checker lifecycle**: the checker is created before
  `depends_on: service_healthy` waiters poll it, so they see `starting` instead
  of nil. Don't reorder checker creation after `waitForDeps`.
- **State dirs** are per-project and follow XDG. Never hardcode `/tmp/...` or
  `~/.local/...`; always go through `project.Resolve` / `Locations` (per-project)
  or `project.ResolveDaemon` / `DaemonLocations` (daemon-level).

## Common tasks

- **Add a CLI command**: see [docs/adding-a-command.md](docs/adding-a-command.md).
- **Add a config field**: see [docs/config-schema.md](docs/config-schema.md).
- **Change the wire protocol**: see [docs/control-protocol.md](docs/control-protocol.md).
- **Implement the web UI**: the web UI is a WS frontend over the same control
  socket. See `internal/web/server.go` and the roadmap for remaining items.

## Roadmap

See [docs/roadmap.md](docs/roadmap.md). Core, health, TUI, global daemon, web
UI, and autostart are done; several polish items remain.
