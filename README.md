# local-compose

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Go Report Card](https://goreportcard.com/badge/github.com/blesswinsamuel/local-compose)](https://goreportcard.com/report/github.com/blesswinsamuel/local-compose)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> Orchestrate local processes the way you orchestrate containers with docker-compose — without Docker.

`local-compose` runs the processes in your `local-compose.yml` on your own machine, with the
ergonomics you already know: `up`, `down`, `ps`, `logs`, `restart`, `build`, and an interactive
TUI. It's a single static binary, built for macOS and Linux, that supervises your dev services,
streams their output, restarts them on crash, and waits for healthchecks before starting dependents.

Use it for the "run a few processes together" half of docker-compose — the API server, the web
dev server, a local DB, a worker — without spinning up a container runtime.

---

## Why?

`docker-compose` is great for reproducing production, but for everyday local dev it drags in a
container runtime, images, builds, volumes, and a network — when all you really want is "start
these three commands and show me their logs together." Existing lightweight tools (`mprocs`,
`overmind`, `hivemind`, `foreman`) cover pieces of this, but none combine a compose-style config,
a detached lifecycle (`up -d` / `ps` / `down`), health-gated dependencies, and a TUI in one tool.

`local-compose` fills that gap:

- **Compose-style config** — a single `local-compose.yml` you already know how to read.
- **Detached lifecycle** — `up -d` runs a background supervisor you can talk back to with `ps`,
  `logs`, `restart`, and `down`.
- **Process-group safety** — each service runs in its own process group, so `down` never leaves
  orphans behind (the bug most lightweight supervisors have).
- **Health-gated dependencies** — `depends_on: { condition: service_healthy }` waits for a
  healthcheck to pass before starting dependents.
- **A real TUI** — drive everything from the terminal with live logs and one-key actions.
- **One static binary** — no runtime, no daemon-on-a-daemon, no container engine.

## Installation

### From source (requires Go 1.26+)

```bash
go install github.com/blesswinsamuel/local-compose/cmd/local-compose@latest
```

### Build from a clone

```bash
git clone https://github.com/blesswinsamuel/local-compose.git
cd local-compose
go build -o local-compose ./cmd/local-compose
```

Prebuilt binaries for tagged releases will be published on the
[releases page](https://github.com/blesswinsamuel/local-compose/releases) once available.

## Quick start

Create a `local-compose.yml` in your project:

```yaml
version: "1"
name: myapp
services:
  api:
    command: cargo run --bin api
    working_dir: ./api
    build: cargo build --bin api          # run once before first up; --build forces
    depends_on:
      db: { condition: service_healthy }  # wait for db's healthcheck to pass
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 5s
      retries: 10
      timeout: 2s
  web:
    command: pnpm dev
    working_dir: ./web
    depends_on: [api]
    build:
      command: pnpm build
      working_dir: ./web
      env:
        NODE_ENV: production
      shell: bash
  db:
    command: postgres -D /usr/local/var/postgres
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 5s
      retries: 5
```

Then:

```bash
local-compose up -d        # start everything in the background
local-compose ps           # see status, pids, health
local-compose logs -f api  # tail the api's output
local-compose restart web  # restart one service
local-compose tui          # interactive dashboard
local-compose down         # stop everything
```

## Commands

| Command | Description |
| --- | --- |
| `up [-d] [--build]` | Start all services (in dependency order). `-d` detaches into a background supervisor. `--build` runs build steps first. |
| `down` | Stop all services and the supervisor. Idempotent — safe to run when nothing's up. |
| `ps` | List services with status, PID, restart count, health, and exit code. |
| `logs [service] [--follow]` | Print a service's logs. `--follow` streams. With no service, defaults to the first one. |
| `restart [service]` | Restart one service, or all when no service is given. |
| `build [service...]` | Run build commands for the named services (or all that declare one, in start order). |
| `tui` | Open the interactive terminal UI. |

Global flags:

| Flag | Description |
| --- | --- |
| `-f, --file <path>` | Path to `local-compose.yml` (default: walk up from the current directory). |
| `-p, --project <name>` | Project name (default: the `name:` field, else the config file's directory name). |

### The TUI

```
┌─ Services ─────────────── ┐ ┌─ Logs: api ───────────────────────────────────┐
│ SERVICE      STATUS    PID │ │                                                │
│ ▸ api        running  4821 │ │  INFO listening on :8080                       │
│   web        running  4822 │ │  INFO connected to db                          │
│   db         healthy  4820 │ │  ...                                           │
└────────────────────────── ┘ └────────────────────────────────────────────────┘
 ↑/↓ select · Tab pane · r restart · s stop · d down · q quit
```

| Key | Action |
| --- | --- |
| `↑` / `↓` (or `k` / `j`) | Select a service |
| `g` / `G` | Jump to top / bottom |
| `Enter` / `→` / `l` | Focus the logs pane |
| `←` / `h` | Back to the service list |
| `Tab` | Switch panes |
| `r` | Restart the selected service |
| `s` | Stop the selected service |
| `d` | Stop everything (`down`) |
| `q` / `Ctrl+C` / `Esc` | Quit (detaches only — the supervisor keeps running) |

If you start the TUI with no supervisor running, it offers to start one detached (like `up -d`)
and then attaches.

## Config reference

Top-level:

| Field | Description |
| --- | --- |
| `version` | **Required.** Config schema version (currently `"1"`). |
| `name` | Optional project name. Defaults to the config file's directory name. |
| `services` | **Required.** Map of service name → `Service`. |

`Service`:

| Field | Description |
| --- | --- |
| `command` | **Required.** Shell command to run (executed via `sh -c` by default). |
| `working_dir` | Working directory (relative to the config file). |
| `env` | Map of environment variables. **Additive** over the parent process env. |
| `shell` | Shell used to run `command` (default `sh`). |
| `depends_on` | List of names, or a map of `name: { condition: ... }`. Conditions: `service_started` (default), `service_healthy`. |
| `healthcheck` | Periodic probe; see below. |
| `restart` | `no` (default), `on-failure`, `always`, or `unless-stopped`. |
| `build` | A pre-start build step. Accepts a **string** (`build: cargo build`) or an **object** (see below). |

`build` object form:

```yaml
build:
  command: pnpm build          # required
  working_dir: ./web           # optional, relative to the config file
  env:                         # optional, additive over parent env
    NODE_ENV: production
  shell: bash                  # optional, default sh
```

`build` runs once before the service starts on `up`. `local-compose build` runs every service's
declared build step; `up --build` forces a rebuild. A failed build aborts `up` so services never
start on top of a broken build.

`healthcheck`:

| Field | Description |
| --- | --- |
| `test` | **Required.** Either `["CMD", "executable", "args..."]` (run directly) or `["CMD-SHELL", "command"]` (run via shell). |
| `interval` | Time between probes (default `5s`). |
| `timeout` | Per-probe timeout (default `2s`). |
| `retries` | Consecutive successes/failures that flip the state (default `3`). |

A service starts `starting`, becomes `healthy` when a probe succeeds, and `unhealthy` after
`retries` consecutive failures. A dependent with `condition: service_healthy` won't start until
its dependency is `healthy`.

### What's deliberately not in the schema

`image`, `build:` (Docker context), `volumes`, `networks`, and `ports` are intentionally absent.
Processes bind ports and read the filesystem directly — there's nothing to map. This keeps the
config honest about what `local-compose` actually does.

## How it works

`local-compose` has a small, focused architecture: one **supervisor** process owns the child
processes and exposes a **Unix socket** control protocol; every other command (`ps`, `logs`,
`restart`, `down`) and the TUI are thin clients over that socket.

```
local-compose up -d  ──►  Supervisor (setsid, backgrounded)
                           │  owns child processes (one process group each)
                           │  serves a Unix socket
                           ▼
            $XDG_RUNTIME_DIR/local-compose/<project>/supervisor.sock

local-compose ps / logs / restart / down / tui  ──►  socket client
```

- **Foreground `up`** is the supervisor staying attached to your terminal.
- **`up -d`** re-execs the binary as a daemonized supervisor (via `setsid`) and returns; it writes
  a pidfile and binds the control socket.
- **Process groups**: each service is started with `Setpgid`, so `down`/`stop` uses `killpg` to
  tear down the whole tree — no orphaned children, even when `command` is a shell pipeline.
- **Restart policy**: `on-failure` only restarts non-zero exits; `unless-stopped` honors an
  explicit `stop` and won't auto-resume on the next `up`. Backoff is exponential with jitter.

### Where state lives

Per the XDG base directory spec:

| Path | Holds |
| --- | --- |
| `$XDG_RUNTIME_DIR/local-compose/<project>/` (or `/tmp/local-compose/<project>/`) | Control socket + pidfile. Transient — cleared on reboot. |
| `$XDG_STATE_HOME/local-compose/<project>/` (or `~/.local/state/local-compose/<project>/`) | Per-service log files + the `unless-stopped` marker. Persisted. |

Each project is namespaced by project name, so multiple projects can run side by side.

## Comparison

| | local-compose | docker-compose | mprocs | overmind / hivemind | foreman / honcho |
| --- | :-: | :-: | :-: | :-: | :-: |
| Compose-style YAML config | ✅ | ✅ | — | — | Procfile |
| Runs locally (no engine) | ✅ | — | ✅ | ✅ | ✅ |
| Detached `up -d` + `ps`/`down` | ✅ | ✅ | — | — | partial |
| Health-gated `depends_on` | ✅ | ✅ | — | — | — |
| Interactive TUI | ✅ | — | ✅ | — | — |
| Restart policies | ✅ | ✅ | — | — | — |
| Single static binary | ✅ | — | ✅ | ✅ | depends on runtime |

## Roadmap

- [x] Core: `up`, `down`, `ps`, `logs`, `restart`, `build`, detached `up -d`
- [x] Healthchecks + `depends_on` conditions
- [x] Bubble Tea TUI
- [ ] `local-compose web` — a browser dashboard (HTTP + WebSocket) over the same control protocol,
      loopback-only by default, assets embedded so the binary stays single-file
- [ ] `.env` / `--env-file` loading and `${VAR}` interpolation in config
- [ ] Log rotation and `logs --tail` / `--since`
- [ ] Graceful stop timeout (SIGTERM → SIGKILL)
- [ ] Shell completions and `local-compose version`
- [ ] Prebuilt release binaries

See [docs/roadmap.md](docs/roadmap.md) for the full breakdown and non-goals.

## Contributing

Contributions are welcome. The project is a standard Go module laid out as a `cmd/` entrypoint
over `internal/` packages (`config`, `dag`, `supervisor`, `daemon`, `control`, `protocol`,
`health`, `logs`, `tui`, `ui`).

```bash
git clone https://github.com/blesswinsamuel/local-compose.git
cd local-compose
go build ./...                     # build
go vet ./...                       # vet
gofmt -l .                         # should print nothing
golangci-lint run                  # lint (optional but recommended)
go test ./...                      # unit + integration tests
```

Before opening a PR:

- Run `gofmt`, `go vet`, and `go test ./...` and make sure they're clean.
- Keep commits focused and use [conventional commit](https://www.conventionalcommits.org/)
  messages (`feat(tui): ...`, `fix(supervisor): ...`, `test(config): ...`).
- Add tests for new behavior — there are unit tests per package and a black-box integration suite
  in `test/integration/`.

Please open an issue first for larger changes so we can align on direction.

## License

[MIT](LICENSE). A `LICENSE` file is included in this repository.
