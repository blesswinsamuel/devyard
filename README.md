<p align="center"><img src="docs/logo.svg" alt="devyard logo" width="96" height="96"></p>

<h1 align="center">devyard</h1>

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> One dev hub for all your projects and services — instead of processes scattered across a pile of terminal tabs.

`devyard` runs your local dev services — API servers, web dev servers, databases, workers — from
a small `devyard.yml`, without containers. Register as many projects as you like; one
daemon supervises all of them, streams their logs, restarts them on crash, gates dependents on
healthchecks, exposes them at named URLs, and gives you a fast web dashboard with terminals,
interactive tasks and a git view.

<img width="1406" height="1154" alt="image" src="https://github.com/user-attachments/assets/f120c6e7-c2cf-4e24-bfc7-fead0468a264" />

## Why?

`docker compose` is great for reproducing production, but for everyday local dev it drags in a
container runtime when all you want is "start these commands, show me their logs, keep them
running". Lightweight tools (`mprocs`, `overmind`, `foreman`) cover pieces of that. devyard
combines:

- **A small config** — one `devyard.yml`, with a JSON Schema for editor completion
  (`devyard schema`).
- **Services that survive** — every process runs under its own tiny runner, so restarting (or
  crashing) the daemon never kills your services; the next daemon simply re-adopts them.
- **Readiness-gated dependencies** — `depends_on: [db]` waits until `db`'s `ready` probe
  (`http`, `tcp` or `exec`) passes.
- **Restarts on crash** by default, with backoff, and a stop that always escalates
  (stop signal → SIGKILL).
- **No port juggling** — `port: auto` picks a free port, passes it as `$PORT`, and other services
  reference it as `${api.url}`.
- **Your shell's environment** — services run with the environment of the shell that started
  them (PATH, version managers, …), not whatever the daemon happened to inherit.
- **Named URLs** — `http://web.myapp.localhost:8080` via a built-in reverse proxy (optional TLS).
- **Interactive tasks** — `devyard run seed` prompts right in your terminal (or in the browser);
  task runs belong to the daemon, so closing a tab or losing a connection doesn't kill them.
- **Web dashboard** — every project at a glance, merged searchable logs, CPU/memory sparklines,
  terminals, a git view, and a ⌘K command palette.
- **One static binary**, macOS and Linux.

## Installation

```bash
go install github.com/blesswinsamuel/devyard/cmd/devyard@latest
```

From a clone (the web UI is embedded, so build it first):

```bash
git clone https://github.com/blesswinsamuel/devyard.git && cd devyard
(cd web && bun install && bun run build)
go build -ldflags "-X github.com/blesswinsamuel/devyard/internal/cli.Version=$(git describe --tags --always)" \
  -o devyard ./cmd/devyard
```

## Quick start

```yaml
# devyard.yml
name: myapp
primary: web                         # myapp.localhost:8080 → web
services:
  db:
    run: postgres -D ./data
    port: 5432
    ready: { tcp: {} }
  api:
    run: cargo run --bin api
    dir: ./api
    port: auto                       # → $PORT, api.myapp.localhost:8080
    depends_on: [db]                 # waits until db is ready
    ready: { http: { path: /health } }
    build:
      run: cargo build --bin api
      sources: [api/src/**, Cargo.lock]   # rebuild on start when these change
  web:
    run: pnpm dev --port $PORT
    dir: ./web
    port: auto
    env: { API_URL: "${api.url}" }
    depends_on: [api]
tasks:
  migrate: cargo run --bin migrate
  seed: node scripts/seed.js         # tasks get a TTY, so prompts work
```

```bash
devyard start            # register the project and start everything (starts the daemon)
devyard status           # status, pid, uptime, restarts, URLs
devyard logs -f          # merged logs of all services (or: devyard logs api web)
devyard restart api      # restart one service
devyard run seed         # run a task interactively; exit code = task's
devyard web --open       # open the dashboard
devyard stop             # stop the project (it stays stopped across daemon restarts)
```

## Commands

| Command | What it does |
| --- | --- |
| `start [svc...] [-f] [--build]` | Register the project (capturing your shell's environment) and start its services (except `autostart: false` ones), or the named ones plus their `depends_on` chain. `-f` follows logs and stops on Ctrl-C. |
| `stop [svc...]` | Stop the project (it won't autostart) or the named services. |
| `restart [svc...] [--build]` | Restart the project's services or the named ones. |
| `reload` | Re-read `devyard.yml`: added services start, removed ones stop, changed ones restart, the rest keep running. Also refreshes the captured environment. |
| `status [-a]` (`ps`) | Service status (`-a`: all projects; `-o json` for scripts). |
| `logs [svc...] [-f] [--tail N] [--previous]` | Logs; merged with name prefixes when several services. |
| `top [name...]` | CPU and memory of running services and tasks. |
| `kill [svc...] [-s SIGNAL]` | Send a signal (default SIGKILL); a killed service stays down. |
| `run <task> [args...]` | Run a task; interactive when attached to a terminal, stdin streamed otherwise. |
| `attach <svc\|task>` | Attach to a running TTY service or task (Ctrl-] detaches). |
| `build [svc...]` | Run build steps in the foreground. |
| `web [--open]` | Print or open the dashboard URL. |
| `schema` | Print the JSON Schema of `devyard.yml`. |
| `project list\|add\|start\|stop\|restart\|reload\|remove\|logs` | Manage registered projects (`remove` deletes state and logs). |
| `service …` / `task list\|run\|stop\|kill\|logs` | Resource-scoped variants of the above. |
| `daemon start\|stop\|restart [-r]\|status` | Manage the daemon. `restart` keeps services running unless `-r`. |

Global flags: `--file <path>` (config; default: search upwards), `-p <project>` (a registered
project id — unknown ids are an error), `-o table|json`.

## Configuration

See [docs/config-schema.md](docs/config-schema.md) for every field. Highlights:

- **Services**: `run` (a string runs with `sh -c`, a list is executed directly), `dir`, `env`,
  `env_files`, `depends_on`, `ready` (`http` / `tcp` / `exec`), `restart` (`never` /
  `on-failure` (default) / `always`), `build` (with optional `sources`), `tty`, `port` / `ports`
  (numbers or `auto`), `host`, `stop` (`signal`, `timeout`), `autostart`.
- **Tasks**: `run`, `dir`, `env`, `env_files`, `depends_on`, `tty` (default `true`).
- **Project**: `name`, `primary`, `env`, `links`, and `env_files` (default `.env` then
  `.env.local`), which feed `${VAR}` interpolation and every process's environment.
- **`devyard.local.yml`** next to the config is merged over it, for machine-specific tweaks.
- **Global config** (`~/.config/devyard/config.yml`): dashboard and proxy listeners, domain
  suffix, TLS, and `web.allowed_hosts`.

## How it works

```
devyard CLI ─┐                    ┌─ runner ─ service
web UI ──────┼─▶ daemon ─ engine ─┼─ runner ─ task
             │  (api, proxy, UI)  └─ runner ─ terminal
             └─ unix socket / http
```

One daemon owns every project. Each project, service and task is an actor that processes
commands one at a time, so concurrent commands never race. Every process runs under its own
`devyard --runner` process that owns its output, PTY and exit status — the daemon can come and
go without affecting anything it supervises. Clients observe state through a single revisioned
stream (`Watch`). Details: [docs/architecture.md](docs/architecture.md),
[docs/control-protocol.md](docs/control-protocol.md).

State lives under the XDG directories: `~/.local/state/devyard/` (projects, logs, daemon log),
`$XDG_RUNTIME_DIR/devyard/` (sockets; falls back to the state dir on macOS) and
`~/.config/devyard/config.yml`.

The dashboard is served at `http://127.0.0.1:9090` (and through the proxy at
`http://devyard.localhost:8080`). It only answers to loopback names, IP addresses, `devyard`,
names under the proxy domain suffix and `web.allowed_hosts`, which blocks DNS-rebinding attacks.
Binding it to a non-loopback address exposes terminals to your network — only do that on a
network you trust.

## Comparison

| | devyard | docker compose | mprocs | overmind | foreman |
| --- | :-: | :-: | :-: | :-: | :-: |
| Compose-style YAML | ✅ | ✅ | — | — | Procfile |
| No container engine | ✅ | — | ✅ | ✅ | ✅ |
| Detached lifecycle (`start`/`status`/`stop`) | ✅ | ✅ | — | — | partial |
| Services survive supervisor restarts | ✅ | ✅ | — | — | — |
| Multi-project | ✅ | ✅ | — | — | — |
| Health-gated dependencies | ✅ | ✅ | — | — | — |
| Web UI, terminals, interactive tasks | ✅ | — | — | — | — |

## Contributing

```bash
go build ./... && go vet ./... && gofmt -l .
golangci-lint run
go test -race ./...                        # unit + hermetic e2e suites (test/e2e)
(cd web && bun run typecheck && bun run test && bun run build)
```

The e2e suites build the real binary and run it in a sandbox (isolated HOME, XDG dirs, git
config and ports) — they never touch your own daemon. `scripts/e2e-linux.sh` runs them in a
Linux container. See [AGENTS.md](AGENTS.md) for conventions and gotchas, and
[docs/roadmap.md](docs/roadmap.md) for what's next.

## License

[MIT](LICENSE).
