# local-compose

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Go Report Card](https://goreportcard.com/badge/github.com/blesswinsamuel/local-compose)](https://goreportcard.com/report/github.com/blesswinsamuel/local-compose)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> Orchestrate local processes the way you orchestrate containers with docker-compose — without Docker.

`local-compose` runs the processes in your `local-compose.yml` on your own machine, with the
ergonomics you already know: `up`, `down`, `ps`, `top`, `logs`, `restart`, `build`, and a
browser-based web UI. It's a single static binary, built for macOS and Linux, that
supervises your dev services, streams their output, restarts them on crash, and waits for
healthchecks before starting dependents.

Use it for the "run a few processes together" half of docker-compose — the API server, the web
dev server, a local DB, a worker — without spinning up a container runtime.

---

## Why?

`docker-compose` is great for reproducing production, but for everyday local dev it drags in a
container runtime, images, builds, volumes, and a network — when all you really want is "start
these three commands and show me their logs together." Existing lightweight tools (`mprocs`,
`overmind`, `hivemind`, `foreman`) cover pieces of this, but none combine a compose-style config,
a detached lifecycle (`up -d` / `ps` / `down`), health-gated dependencies, and a web UI in one tool.

`local-compose` fills that gap:

- **Compose-style config** — a single `local-compose.yml` you already know how to read.
- **Detached lifecycle** — `up -d` runs a background supervisor you can talk back to with `ps`,
  `top`, `logs`, `restart`, and `down`.
- **Process-group safety** — each service runs in its own process group, so `down` never leaves
  orphans behind (the bug most lightweight supervisors have).
- **Health-gated dependencies** — `depends_on: { condition: service_healthy }` waits for a
  healthcheck to pass before starting dependents.
- **Web UI** — a browser dashboard over the same control protocol, with xterm.js log streaming.
- **Multi-project daemon** — a single daemon manages multiple projects; autostarts every
  registered project on daemon startup (unless stopped with `down`).
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

`version`, `commit`, and `date` build metadata can be injected via `-ldflags`
(the defaults are `dev`, empty, empty):

```bash
go build -ldflags "-X github.com/blesswinsamuel/local-compose/internal/cli.Version=v1.2.3 \
                   -X github.com/blesswinsamuel/local-compose/internal/cli.Commit=$(git rev-parse --short HEAD) \
                   -X github.com/blesswinsamuel/local-compose/internal/cli.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
         -o local-compose ./cmd/local-compose
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
    restart: always
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
local-compose logs -f api        # tail the api's output
local-compose logs --tail 100 api # last 100 lines only
local-compose logs --previous api # inspect the previous run's logs
local-compose restart web  # restart one service
local-compose down         # stop the current project
local-compose stop-daemon  # stop all projects and the daemon
```

## Commands

### Project-level Commands
Commands operating on a project context (defaults to `local-compose.yml` in cwd, or explicitly via `-p <name>` / `-f <path>`):

| Command | Description |
| --- | --- |
| `ls` | List all projects managed by the daemon (both running and stopped). |
| `up [-d] [--build]` | Start all services in the project (in dependency order). `-d` detaches. `--build` runs build steps first. Auto-starts the daemon if needed. |
| `down` | Stop all services in the project. Idempotent — safe to run when nothing's up. |
| `ps [-p project]` | List services in the project with status, PID, restart count, and health. |
| `top [service]` | Show CPU and memory usage of each service's process group, sampled over ~1s. `[service]` narrows the view to one service. |
| `remove` / `rm` | Stop and completely remove a project state from the daemon. |

### Service & Interactive Commands

| Command | Description |
| --- | --- |
| `start [service]` | Start or resume one service, or all services in the project. |
| `stop [service]` | Stop one service in place, or all services in the project when omitted. |
| `restart [service]` | Restart one service, or all services in the project when omitted. |
| `kill [service] [-s signal]` | Forcefully terminate one service, or all services in the project when omitted. Sends `SIGKILL` by default; pick another signal with `-s/--signal` (e.g. `SIGTERM`). |
| `logs [service] [-f] [--tail N] [--previous]` | Output or tail logs for a service (or all services in the project). `--tail N` limits history to the last N lines (`0` / omitted = all, still byte-capped). `--previous` shows the immediately preceding run's log instead of the current one (each spawn starts a fresh log; the finished run's is kept as `<service>.prev.log`). |
| `build [service...]` | Run build commands for named services (or all services with build steps). |

### Daemon Commands

| Command | Description |
| --- | --- |
| `daemon start` | Start the global background daemon. (Usually auto-started by `up`). |
| `daemon stop` | Stop the global daemon and all managed projects. |
| `daemon restart` | Seamlessly reload/restart the global daemon process. |
| `daemon status` | Display current daemon status and active projects. |
| `version` | Print the local-compose version. Also available as `local-compose --version`. |

### Global Flags

| Flag | Description |
| --- | --- |
| `-f, --file <path>` | Path to `local-compose.yml` (default: walk up from current directory). |
| `-p, --project <name>` | Resolve a registered project by name from any directory when no `local-compose.yml` is found. When a config file is available, its declared `name` is authoritative. |
| `--env-file <path>` | Path to an env file for variables and config interpolation (default: `.env` next to the config file). |

### The Web UI

The web UI is a browser-based dashboard over the same control protocol — project/service
listing, live xterm.js logs, and start/stop/restart actions. Start it with `local-compose web`
and open the printed URL (loopback-only by default).

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
| `restart` | `no` (default), `on-failure`, or `always`. |
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

### Env files and interpolation

A `.env` file next to `local-compose.yml` (or the file given via `--env-file`) is loaded
automatically. Its variables are passed to every service process — layered under the service's
own `env` (service `env` wins) — and are available for **config interpolation**:

```yaml
# .env
PORT=8080

# local-compose.yml
services:
  api:
    command: cargo run --bin api -- --port ${PORT:-3000}
```

`${VAR}` and `${VAR:-default}` (also `${VAR-default}`) are expanded anywhere in the config text.
Unset variables without a default expand to empty with a warning; `$$` escapes a literal `$`;
bare `$VAR` is left untouched for the shell.

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

`local-compose` has a small, focused architecture: one **global daemon** process owns a
supervisor per project, and exposes a single **Unix socket** control protocol; every other
command (`ps`, `logs`, `restart`, `down`) and the web UI are thin clients over that
socket.

```
local-compose up  ──►  ensureDaemon()  ──►  Global Daemon (setsid, backgrounded)
                                              │  owns one Supervisor per project
                                              │  serves one Unix socket
                                              │  optionally serves web UI
                                              ▼
                            $XDG_RUNTIME_DIR/local-compose/daemon.sock

local-compose ps / logs / restart / down / web  ──►  socket client
```

- **`up`** auto-starts the daemon if it's not running, sends `start_project` over the socket,
  and (in foreground mode) follows logs from all services. `up -d` just starts the project and
  returns.
- **Autostart**: on daemon startup, every registered project is started automatically
  (unless explicitly stopped with `down` / `stop`, which writes a project `.stopped` marker).
  `start <svc>` on a stopped project lazily starts just that service (plus its `depends_on` chain).
- **Process groups**: each service is started with `Setpgid`, so `down`/`stop` uses `killpg` to
  tear down the whole tree — no orphaned children, even when `command` is a shell pipeline.
- **Restart policy**: `on-failure` only restarts non-zero exits (capped); `always` restarts
  indefinitely. `stop <svc>` is ephemeral — the running supervisor honors it, but the next
  **daemon autostart** resumes the service unless the whole project was stopped.

### Where state lives

Per the XDG base directory spec:

| Path | Holds |
| --- | --- |
| `$XDG_RUNTIME_DIR/local-compose/` (or `/tmp/local-compose/`) | Daemon control socket + pidfile. Transient — cleared on reboot. |
| `$XDG_STATE_HOME/local-compose/<project>/` (or `~/.local/state/local-compose/<project>/`) | Per-service log files, config-path, and the project `.stopped` marker. Persisted. |
| `$XDG_CONFIG_HOME/local-compose/config.yml` (or `~/.config/local-compose/config.yml`) | Global config (web UI settings). |

Each project is namespaced by project name, so multiple projects can run side by side under one
daemon.

### Web UI

Start a browser-based dashboard (SolidJS SPA with xterm.js log streaming) with
`local-compose web`. It connects to a running daemon over the control socket.

```bash
local-compose web                  # http://127.0.0.1:9090
local-compose web --port 8080
```

Optional defaults in the global config:

```yaml
# ~/.config/local-compose/config.yml
web:
  host: 127.0.0.1   # loopback only by default
  port: 9090
```

## Comparison

| | local-compose | docker-compose | mprocs | overmind / hivemind | foreman / honcho |
| --- | :-: | :-: | :-: | :-: | :-: |
| Compose-style YAML config | ✅ | ✅ | — | — | Procfile |
| Runs locally (no engine) | ✅ | — | ✅ | ✅ | ✅ |
| Detached `up -d` + `ps`/`top`/`down` | ✅ | ✅ | — | — | partial |
| Multi-project daemon | ✅ | ✅ | — | — | — |
| Health-gated `depends_on` | ✅ | ✅ | — | — | — |
| Web UI | ✅ | — | — | — | — |
| Restart policies | ✅ | ✅ | — | — | — |
| Single static binary | ✅ | — | ✅ | ✅ | depends on runtime |

## Roadmap

- [x] Core: `up`, `down`, `ps`, `top`, `logs`, `restart`, `build`, detached `up -d`
- [x] Healthchecks + `depends_on` conditions
- [x] Global daemon with multi-project orchestrator
- [x] Autostart projects based on service restart policies
- [x] Web UI — browser dashboard (WS + embedded SolidJS SPA with xterm.js logs)
- [x] Global config (`web.host`, `web.port` for `local-compose web`)
- [x] Log rotation (current + previous run per service, `logs --previous`; size-based soft cap)
- [ ] `.env` / `--env-file` loading and `${VAR}` interpolation in config
- [x] `logs --tail N` (server-side; web default to 5000)
- [ ] `logs --since`
- [ ] Graceful stop timeout (SIGTERM → SIGKILL)
- [ ] `on-failure` autostart
- [ ] Shell completions and `local-compose version`
- [ ] Prebuilt release binaries

See [docs/roadmap.md](docs/roadmap.md) for the full breakdown and non-goals.

## Contributing

Contributions are welcome. The project is a standard Go module laid out as a `cmd/` entrypoint
over `internal/` packages (`config`, `dag`, `supervisor`, `procstat`, `daemon`, `orchestrator`, `control`,
`protocol`, `health`, `logs`, `ui`, `web`, `globalconfig`, `project`).

```bash
git clone https://github.com/blesswinsamuel/local-compose.git
cd local-compose
go build ./...                     # build
go vet ./...                       # vet
gofmt -l .                         # should print nothing
golangci-lint run                  # lint (config in .golangci-lint.yml)
go test ./...                      # unit + integration tests
```

Before opening a PR:

- Run `gofmt`, `go vet`, and `go test ./...` and make sure they're clean.
- Keep commits focused and use [conventional commit](https://www.conventionalcommits.org/)
  messages (`feat(web): ...`, `fix(supervisor): ...`, `test(config): ...`).
- Add tests for new behavior — there are unit tests per package and a black-box integration suite
  in `test/integration/`.

Please open an issue first for larger changes so we can align on direction.

## License

[MIT](LICENSE). A `LICENSE` file is included in this repository.
