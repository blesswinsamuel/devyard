# AGENTS.md

Guidance for AI coding agents (and human contributors) working in this repo.
Read this before making changes.

## What this project is

`devyard` is a docker-compose-style CLI that orchestrates **local processes**
(no Docker). macOS + Linux only. Written in Go 1.26. Module:
`github.com/blesswinsamuel/devyard`. See [README.md](README.md) for the user
pitch and [docs/architecture.md](docs/architecture.md) for how it works inside.

## Greenfield Project & Compatibility Strategy

This is an early-stage **greenfield project**.
- **No backward compatibility**: Do not add or keep legacy aliases, backward-compatibility shims, or deprecated command flags.
- **Clean breaking refactors**: Prefer direct, clean breaking changes over preserving outdated interfaces or command aliases.

## Essential commands

```bash
go build ./...                      # build everything
go build -o devyard ./cmd/devyard   # build the binary
go vet ./...                        # vet
gofmt -l .                          # must print nothing
go test ./...                       # all unit + e2e tests
go test -race ./...                 # with the race detector (CI uses this)
go test ./internal/engine/...       # one package
go test ./test/e2e/...              # the hermetic black-box e2e suites (build the real binary)
golangci-lint run                   # lint; config in .golangci-lint.yml (v2)
```

**Web UI build (embedded SPA).** The frontend source lives in `web/` (SolidJS +
Vite, built with `bun`). It is embedded into the Go binary via
`//go:embed all:dist` in `internal/web/web.go`. Built frontend assets
in `internal/web/dist/` are gitignored (except `.gitkeep` so Go builds and
linters work on clean checkouts) — CI builds it in the "build & test" job
(`bun install && bun run build`, which outputs to `../internal/web/dist`)
before `go build ./...`. For local development, `devyard start` (see
`devyard.yml`) runs `bun run dev` (a Vite server on :19095 proxying to the
Go web proxy on :9090), so you can edit `web/src/**` and see changes live with
no rebuild. When changing web source, always verify the build still compiles:
`cd web && bun install && bun run build`.

**Zaidan-generated UI components (do not edit).** Everything in
`web/src/components/ui/` is generated from the [zaidan](https://zaidan.carere.dev)
registry (config in `web/components.json`) and must never be hand-modified —
edits would be silently overwritten by regeneration. To customize or extend a
component, create a wrapper in `web/src/components/` (e.g. `badge.tsx`,
`toaster.tsx`, `tab-strip.tsx`) that composes the generated primitive, and point
consumers at the wrapper.

Tests and lint must stay green. CI (`.github/workflows/ci.yml`) runs `go vet`,
`gofmt`, `golangci-lint`, `go build`, and `go test -race` on Ubuntu and macOS.
The e2e suites (`test/e2e`) build the actual binary and drive the CLI, the API
and the web endpoints inside a sandbox (isolated HOME, XDG dirs, git config,
ephemeral ports, process-leak checks) — they never touch the user's real
daemon or state. `scripts/e2e-linux.sh` runs them in a Linux container.

## Repo layout

```
cmd/devyard/main.go   # entrypoint -> cli.Execute() (also dispatches --daemon / --runner)
internal/
  cli/         # cobra commands; thin client of the daemon API
  client/      # control-socket client (unencrypted HTTP/2 over the unix socket)
  daemon/      # daemon lifecycle (lock, pidfile, spawn, handover), presenter, proxy routes
  api/         # DaemonService handlers (Watch, Logs, Attach, Stats, git, ...)
  engine/      # project/service/task actors, manager, registration, config -> definitions
  runner/      # per-run supervisor process (stdio/PTY, logs, stop escalation, status, attach)
  logstore/    # per-run structured log files: write, tail, page, follow
  events/      # revisioned state bus behind Watch
  sessions/    # interactive sessions: runner-backed terminals, task/service attach
  web/         # dashboard handler (SPA, API, /ws/attach, Host allowlist) + embedded dist
  gitstate/    # per-project git status on the bus; serialized remote ops
  gitlog/ gitwatcher/  # git commands and repository watching
  config/      # devyard.yml schema, parsing, validation, env layering
  globalconfig/ # $XDG_CONFIG_HOME/devyard/config.yml
  paths/       # XDG path resolution and project-id validation
  health/ dag/ procstat/ ports/ proxy/ ui/
test/e2e/      # hermetic black-box suites (harness, fixtures, cli, api, daemon, ...)
web/           # SolidJS SPA
```

## Architecture in one paragraph

A single **daemon** (`devyard --daemon`) owns an `engine.Manager` of
**project actors**; each project owns **service** and **task actors**. An actor
is one goroutine that owns its entity's state and processes commands in order,
so concurrent calls can never race into duplicate processes or stuck states.
Every process is launched through its own **runner** (`devyard --runner`, a
separate session) that holds its stdio/PTY, writes its logs and records its
exit, so daemon restarts and crashes never affect services: a new daemon just
re-adopts the runners. State reaches clients through a revisioned **bus**
(`Watch`: snapshot + changes). The CLI talks to the daemon over its unix
socket; the web UI over the dashboard listener. See
[docs/architecture.md](docs/architecture.md) and
[docs/control-protocol.md](docs/control-protocol.md).

## Conventions

- **Commit style**: conventional commits — `feat(web): ...`, `fix(engine): ...`,
  `test(config): ...`, `docs: ...`. Keep commits focused and build-clean.
- **Go style**: `gofmt`'d, errors wrapped with `%w` and a leading package context
  (`fmt.Errorf("engine: ...: %w", err)`), `context.Context` for cancellation,
  no `log.Fatal` in packages (only `main`/CLI may exit).
- **No inline imports** beyond the top-of-file block.
- **Process safety**: every child is started with `Setpgid`; teardown uses
  `killpg` (negative pid) so shell pipelines don't leak orphans. Don't add a
  code path that spawns a child without its own process group.
- **Config changes**: update `internal/config` **and** its tests **and**
  [docs/config-schema.md](docs/config-schema.md), then regenerate the JSON
  Schema with `go generate ./internal/config` (a test fails when it is stale).
  Field comments on the config types are user-facing: they become the schema's
  descriptions. Unknown fields should warn, not error.
- **Global config changes**: update `internal/globalconfig/globalconfig.go` and
  its tests. The global config lives at
  `$XDG_CONFIG_HOME/devyard/config.yml`.
- **Protocol changes**: edit `proto/devyard/v1/control.proto`, run
  `buf lint && buf generate`, implement in `internal/api`, update the CLI and
  `web/src/data`, and [docs/control-protocol.md](docs/control-protocol.md).
  The protocol is shared by the CLI and web UI — keep messages
  frontend-agnostic (no terminal-specific fields).
- **Tests**: add unit tests in the package you changed. For cross-cutting
  behavior, prefer a black-box case in `test/e2e/`. Bug fixes get a
  regression test named after the bug.

## Gotchas

- **Never mutate actor state from outside the actor.** Add a message type and
  handle it in the actor loop. Long work (waiting for dependencies, backoff,
  watching a run) runs in goroutines that post events back to the mailbox.
- **Commands must reply only when their effect holds.** Stop replies when the
  process is gone; don't add fire-and-forget variants.
- **Project actors may block on service actors, never the reverse.** Service
  and task actors must not call into their project synchronously.
- **Readers use snapshots.** `Project.View()` and bus entities are immutable;
  don't hand out maps that an actor mutates.
- **The daemon never signals raw pids.** It goes through the runner (`stop`,
  `signal`); the runner signals its own process group. Every child has its own
  process group or session; with a PTY use `Setsid` only (combining it with
  `Setpgid` fails with EPERM).
- **Errors are typed.** Return `engine.Err*` / `ErrSessionNotFound`, wrapped
  with `%w`; `api.toConnect` maps them to Connect codes. Never match error
  strings, on either side.
- **Hidden flags.** `--daemon` and `--runner` are handled in `cli.Execute`
  before cobra. Tests that launch runners from a test binary need a
  `TestMain` hook calling `runner.Main` (see `internal/runner`,
  `internal/engine` tests).
- **Paths.** Always resolve through `internal/paths` and validate project ids
  with `paths.ValidateID`; never hardcode `/tmp` or `~/.local`.
- **Tests must be hermetic.** Unit tests set their own dirs; e2e tests go
  through `test/e2e/harness`, which sandboxes HOME, XDG dirs, git config and
  ports. Never touch the developer's real daemon or config.

## Common tasks

- **Add a CLI command**: see [docs/adding-a-command.md](docs/adding-a-command.md).
- **Add a config field**: see [docs/config-schema.md](docs/config-schema.md).
- **Change the wire protocol**: see [docs/control-protocol.md](docs/control-protocol.md).
- **Implement the web UI**: the web UI is embedded in the binary and served directly
  by the daemon. See `internal/web/web.go` (server side) and `web/src` (SPA).

## Roadmap

See [docs/roadmap.md](docs/roadmap.md). Core, health, global daemon, web
UI, and autostart are done; several polish items remain.
