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
(`up -d`, `ps`, `logs`, `restart`, `down`, `build`, `up --build`) against
isolated XDG dirs — it never touches the user's real state.

## Repo layout

```
cmd/local-compose/main.go   # entrypoint -> cli.Execute()
internal/
  cli/         # cobra commands + flag wiring + the daemon re-exec handler (root.go)
  config/      # local-compose.yml schema, parsing, validation, defaults
  project/     # project name + XDG runtime/state dir resolution
  dag/         # depends_on graph, cycle detection, topo order
  supervisor/  # owns child processes: launch, log capture, restart policy, health, stop
  daemon/      # setsid re-exec for `up -d`, pidfile, liveness checks (build tag: unix)
  control/     # Unix-socket server (Backend interface) + thin Client
  protocol/    # wire frames: Request/Response, length-prefixed JSON
  health/      # per-service healthcheck state machine (starting -> healthy | unhealthy)
  logs/        # doc-only placeholder; actual logging lives in supervisor/logs.go
  tui/         # Bubble Tea v2 frontend (charm.land/bubbletea/v2)
  ui/          # shared status color/label helpers used by cli, tui, (future) web
  web/         # STUB (doc.go only). `local-compose web` is not implemented yet.
test/integration/  # black-box e2e tests
```

## Architecture in one paragraph

`up` runs a **Supervisor** (foreground) that spawns each service in its own
process group, in topological order, and serves a **Unix socket** control
protocol. `up -d` re-execs the binary as a daemonized supervisor
(`internal/daemon`, `setsid` + pidfile) and returns. Every other command
(`ps`, `logs`, `restart`, `down`) and the TUI are thin **Client** connections
over that socket. See [docs/architecture.md](docs/architecture.md) and
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
- **Protocol changes**: update `internal/protocol/protocol.go`, both sides in
  `internal/control`, and [docs/control-protocol.md](docs/control-protocol.md).
  The protocol is shared by the CLI, TUI, and future web UI — keep messages
  frontend-agnostic (no terminal-specific fields).
- **Tests**: add unit tests in the package you changed. For cross-cutting
  behavior, prefer a black-box case in `test/integration/`.

## Gotchas

- **`-f` is the persistent config flag**, so `logs --follow` has no `-f` shorthand
  (it's `--follow`). Don't "fix" this.
- **Daemon re-exec** is detected in `cli.Execute()` *before* cobra runs, via the
  hidden `--supervisor <project>` flag (`daemon.SupervisorFlag`). If you add
  root-level flags that the daemon child must inherit, parse them in
  `runSupervisorChild` (`internal/cli/root.go`), not just in cobra.
- **`unless-stopped`** persists a "stopped" marker file in the state dir so a
  service doesn't auto-resume on the next `up`. `Restart` removes it. If you
  touch restart/stop logic, keep the marker in sync. When a service is skipped
  because of the marker, its dependents are skipped transitively (`waitForDep`
  returns `errDependencyStopped`) and `up` exits 0 — an explicit stop is not a
  failure, so `Failed()` stays false. Only a dependency that genuinely exits or
  goes unhealthy sets `Failed()`; a user-initiated shutdown
  (`errSupervisorStopping`) doesn't either.
- **Health checker lifecycle**: the checker is created before
  `depends_on: service_healthy` waiters poll it, so they see `starting` instead
  of nil. Don't reorder checker creation after `waitForDeps`.
- **State dirs** are per-project and follow XDG. Never hardcode `/tmp/...` or
  `~/.local/...`; always go through `project.Resolve` / `Locations`.
- **`internal/web` is a stub.** Don't reference it as if it works; it's the
  Phase 5 roadmap item.

## Common tasks

- **Add a CLI command**: see [docs/adding-a-command.md](docs/adding-a-command.md).
- **Add a config field**: see [docs/config-schema.md](docs/config-schema.md).
- **Change the wire protocol**: see [docs/control-protocol.md](docs/control-protocol.md).
- **Implement the web UI**: it's a fourth frontend over the same socket — dial
  the control socket from HTTP/WS handlers. See the roadmap.

## Roadmap

See [docs/roadmap.md](docs/roadmap.md). Core, health, and TUI are done; the web
UI and several polish items remain.
