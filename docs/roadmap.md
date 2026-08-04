# Roadmap

What's done, what's planned, and where each item lives in the code.

## Done

- **Core lifecycle** — `up`, `up -d`, `down`, `ps`, `top`, `logs [service] [--follow]`,
  `restart [service]`, `kill [service] [--signal]`, `build [service...]`,
  `up --build`. (`internal/cli`, `internal/supervisor`, `internal/control`)
- **Global daemon** — a single daemon process owns multiple project
  supervisors (`map[string]*supervisor.Supervisor`). All CLI commands, the TUI,
  and the web UI are thin clients over one Unix socket at
  `$XDG_RUNTIME_DIR/local-compose/daemon.sock`. (`internal/orchestrator`,
  `internal/daemon`, `internal/cli/daemon_run.go`)
- **Autostart** — on daemon startup, projects with `restart: always` or
  `restart: unless-stopped` services are started automatically **unless** a
  project-level `.stopped` marker exists (honored for both policies).
  `on-failure` does not trigger autostart. (`internal/orchestrator`
  `Autostart()`)
- **Process-group safety** — each service in its own `setpgid` group; teardown
  via `killpg` so no orphans. (`internal/supervisor/proc_unix.go`)
- **`top` resource view** — `local-compose top [service]` aggregates CPU and
  memory across each service's process group (procfs on Linux, libproc on
  macOS), sampled over a 1s interval for a live CPU%. (`internal/procstat`,
  `internal/supervisor`)
- **Dependency ordering** — `depends_on` graph with cycle detection and
  topological start order. (`internal/dag`)
- **Healthchecks + conditions** — per-service `starting -> healthy | unhealthy`
  state machine; `depends_on: { condition: service_healthy }` gates dependents.
  (`internal/health`, `internal/supervisor`)
- **Restart policies** — `no` / `on-failure` / `always` / `unless-stopped` with
  exponential backoff + jitter; `unless-stopped` persists a stopped marker so
  it doesn't auto-resume. (`internal/supervisor/restart.go`)
- **TUI** — Bubble Tea v2 frontend: project selection screen, service list,
  streaming logs, restart/stop/down keybindings, Esc to go back to project
  list. Works without a config file (shows all known projects). (`internal/tui`)
- **Web UI** — WS server + embedded SolidJS SPA (xterm.js logs). Start with
  `local-compose web` (proxies to the daemon socket). Loopback-only by default.
  (`internal/web`, `web/`)
- **Global config** — `$XDG_CONFIG_HOME/local-compose/config.yml` with
  `web.host`, `web.port` defaults for `local-compose web`. (`internal/globalconfig`)
- **Config discovery** — `-f`/`-p` flags; walk-up discovery of
  `local-compose.yml`. (`internal/config`, `internal/cli`)
- **Env files + interpolation** — `.env` next to the config (or `--env-file`)
  feeds child-process env and `${VAR}` / `${VAR:-default}` config
  interpolation. (`internal/config/envfile.go`, `internal/cli`)
- **Tests** — unit tests per package plus a black-box integration suite that
  builds the real binary and drives the full lifecycle. (`test/integration`)
- **CI + lint** — GitHub Actions workflow (`go vet`, `gofmt`, `golangci-lint`,
  `go build`, `go test -race` on Ubuntu + macOS) and a v2 `.golangci-lint.yml`.

## Planned

- [ ] **Size-based log rotation** in the state dir; `logs --tail N` / `--since`.
      (Run-based rotation already ships: each spawn starts a fresh
      `<service>.log` and the finished run is kept as `<service>.prev.log`,
      inspectable via `logs --previous`.)
- [ ] **Configurable graceful stop timeout** per service / via flag (currently
      fixed at 10s in `Supervisor`).
- [ ] **`on-failure` autostart** — currently `on-failure` does not trigger
      autostart; only `always` and `unless-stopped` do.
- [ ] **Shell completions** (cobra `__complete`) and `local-compose version`.
- [ ] **Strict config mode** that warns on unknown fields (today `yaml.v3`
      silently ignores them).
- [ ] **Prebuilt release binaries** (GoReleaser).

## Non-goals

- Docker compatibility — we will not add `image`, `volumes`, `networks`, or
  `ports` shims. See [config-schema.md](config-schema.md) > "Intentionally
  absent".
- Windows support — the supervisor relies on Unix process groups, signals, and
  a Unix domain socket. A Windows port would need a separate supervisor
  implementation (Job Objects) and transport (named pipes/TCP).
