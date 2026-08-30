# AGENTS.md

Guidance for AI coding agents (and human contributors) working in this repo.
Read this before making changes.

## What this project is

`local-compose` is a docker-compose-style CLI that orchestrates **local processes**
(no Docker). macOS + Linux only. Written in Go 1.26. Module:
`github.com/blesswinsamuel/local-compose`. See [README.md](README.md) for the user
pitch and [docs/architecture.md](docs/architecture.md) for how it works inside.

## Greenfield Project & Compatibility Strategy

This is an early-stage **greenfield project**.
- **No backward compatibility**: Do not add or keep legacy aliases, backward-compatibility shims, or deprecated command flags.
- **Clean breaking refactors**: Prefer direct, clean breaking changes over preserving outdated interfaces or command aliases.

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

**Web UI build (embedded SPA).** The frontend source lives in `web/` (SolidJS +
Vite, built with `bun`). It is embedded into the Go binary via
`//go:embed all:dist` in `internal/web/server.go`. Built frontend assets
in `internal/web/dist/` are gitignored (except `.gitkeep` so Go builds and
linters work on clean checkouts) — CI builds it in the "build & test" job
(`bun install && bun run build`, which outputs to `../internal/web/dist`)
before `go build ./...`. For local development, `local-compose up` (see
`local-compose.yml`) runs `bun run dev` (a Vite server on :5173 proxying to the
Go web proxy on :9090), so you can edit `web/src/**` and see changes live with
no rebuild. When changing web source, always verify the build still compiles:
`cd web && bun install && bun run build`.

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
  procstat/    # process-group CPU/memory sampling for `top` (procfs + libproc)
  supervisor/  # owns child processes: launch, log capture, restart policy, health, stop
  daemon/      # setsid re-exec for the global daemon, pidfile, liveness checks (build tag: unix)
  orchestrator/ # multi-project manager: owns map[string]*supervisor.Supervisor, autostart
  control/     # Unix-socket server (MultiBackend interface) + thin Client
  protocol/    # wire frames: Request/Response, length-prefixed JSON
  health/      # per-service healthcheck state machine (starting -> healthy | unhealthy)
  ui/          # shared status color/label helpers used by cli and web
  web/         # WS server + embedded SolidJS SPA (xterm.js logs)
  globalconfig/ # user-level config ($XDG_CONFIG_HOME/local-compose/config.yml)
test/integration/  # black-box e2e tests
```

## Architecture in one paragraph

A single **global daemon** process (`local-compose --daemon`, spawned by `up`
or `start-daemon`) owns a `map[string]*supervisor.Supervisor` — one supervisor
per project. It serves one **Unix-socket control protocol** at
`$XDG_RUNTIME_DIR/local-compose/daemon.sock`. Every CLI command (`ps`, `logs`,
`restart`, `down`), and the web UI are thin **Client** connections over
that socket. The daemon autostarts every registered project on startup unless a
project-level `.stopped` marker exists. See
[docs/architecture.md](docs/architecture.md) and
[docs/control-protocol.md](docs/control-protocol.md).

## Conventions

- **Commit style**: conventional commits — `feat(web): ...`, `fix(supervisor): ...`,
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
  The protocol is shared by the CLI and web UI — keep messages
  frontend-agnostic (no terminal-specific fields).
- **Tests**: add unit tests in the package you changed. For cross-cutting
  behavior, prefer a black-box case in `test/integration/`.

## Gotchas

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
  project's config, and starts every project **unless** a project-level
  `.stopped` marker exists. Skipped projects are still registered in
  memory as stopped so list commands stay complete. Restart policy does not
  gate autostart.
- **Project-level `.stopped` marker**: `StopProject` writes a
  project-level `$XDG_STATE_HOME/local-compose/<project>/.stopped` marker (so
  autostart won't resume the project) and keeps the project in the daemon map
  (closed supervisor retained for `ps`). Explicit `up`/`start`/`start <svc>`
  removes the marker.
- **`start <svc>` on a stopped project** lazily starts just that service plus
  its transitive `depends_on` chain: the supervisor is materialized with a
  `Selected` service set, and unselected services are registered as `stopped`
  and skipped (logged `skipping service <name>`) — a skip is not a failure, so
  `Failed()` stays false. Only a dependency that genuinely exits or goes
  unhealthy sets `Failed()`; a user-initiated shutdown (`errSupervisorStopping`)
  doesn't either.
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

See [docs/roadmap.md](docs/roadmap.md). Core, health, global daemon, web
UI, and autostart are done; several polish items remain.
