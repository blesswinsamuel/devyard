# Roadmap

What's done, what's planned, and where each item lives in the code.

## Done

- **Core lifecycle** — `up`, `up -d`, `down`, `ps`, `top`, `logs [service] [--follow] [--tail N] [--previous]`,
  `restart [service]`, `kill [service] [--signal]`, `build [service...]`,
  `up --build`. Size-based log rotation (10 MiB soft cap per current run file)
  and server-side history tailing keep huge logs from flooding clients.
  (`internal/cli`, `internal/supervisor`, `internal/control`)
- **Global daemon** — a single daemon process owns multiple project
  supervisors (`map[string]*supervisor.Supervisor`). All CLI commands and the
  web UI are thin clients over one Unix socket at
  `$XDG_RUNTIME_DIR/devyard/daemon.sock`. (`internal/orchestrator`,
  `internal/daemon`, `internal/cli/daemon_run.go`)
- **Autostart** — on daemon startup, every registered project is started
  automatically **unless** a project-level `.stopped` marker exists (written by
  `down` / `stop`). (`internal/orchestrator` `Autostart()`)
- **Process-group safety** — each service in its own `setpgid` group; teardown
  via `killpg` so no orphans. (`internal/supervisor/proc_unix.go`)
- **`top` resource view** — `devyard top [service]` aggregates CPU and
  memory across each service's process group (procfs on Linux, libproc on
  macOS), sampled over a 1s interval for a live CPU%. (`internal/procstat`,
  `internal/supervisor`)
- **Dependency ordering** — `depends_on` graph with cycle detection and
  topological start order. (`internal/dag`)
- **Healthchecks + conditions** — per-service `starting -> healthy | unhealthy`
  state machine; `depends_on: { condition: service_healthy }` gates dependents.
  (`internal/health`, `internal/supervisor`)
- **Restart policies** — `no` / `on-failure` / `always` with
  exponential backoff + jitter. (`internal/supervisor/restart.go`)
- **Reverse proxy / named URLs** — services that declare `port`/`ports` are
  exposed by a daemon-owned reverse proxy at
  `<service>.<project>.localhost[:suffix]:<port>` (project
  `proxy.default_service` serves `project.localhost`; named ports prefix the
  service label). Live route resolution from the orchestrator; styled 502/503
  error pages; URL column in `ps`. (`internal/proxy`, `internal/config`,
  `internal/cli`, `internal/orchestrator`)
- **Web UI** — WS server + embedded SolidJS SPA (xterm.js logs). Start with
  `devyard web` (proxies to the daemon socket). Loopback-only by default.
  (`internal/web`, `web/`)
- **Global config** — `$XDG_CONFIG_HOME/devyard/config.yml` with
  `web.host`, `web.port` defaults for `devyard web`. (`internal/globalconfig`)
- **Config discovery** — `-f`/`-p` flags; walk-up discovery of
  `devyard.yml`. (`internal/config`, `internal/cli`)
- **Env files + interpolation** — `.env` next to the config (or `--env-file`)
  feeds child-process env and `${VAR}` / `${VAR:-default}` config
  interpolation. (`internal/config/envfile.go`, `internal/cli`)
- **Tests** — unit tests per package plus a black-box integration suite that
  builds the real binary and drives the full lifecycle. (`test/integration`)
- **CI + lint** — GitHub Actions workflow (`go vet`, `gofmt`, `golangci-lint`,
  `go build`, `go test -race` on Ubuntu + macOS) and a v2 `.golangci-lint.yml`.

## Planned

- [ ] **Configurable graceful stop timeout** per service / via flag (currently
      fixed at 10s in `Supervisor`).
- [ ] **`on-failure` autostart** — currently autostart resumes every project
      regardless of restart policy; a future refinement could gate it on policy.
- [ ] **Shell completions** (cobra `__complete`) and `devyard version`.
- [ ] **Strict config mode** that warns on unknown fields (today `yaml.v3`
      silently ignores them).
- [ ] **Prebuilt release binaries** (GoReleaser).
- [ ] **`logs --since`** time filter (size-based rotation and `logs --tail N`
      already ship).

## Non-goals

- Docker compatibility — we will not add `image`, `volumes`, `networks`, or
  `ports` shims. See [config-schema.md](config-schema.md) > "Intentionally
  absent".
- Windows support — the supervisor relies on Unix process groups, signals, and
  a Unix domain socket. A Windows port would need a separate supervisor
  implementation (Job Objects) and transport (named pipes/TCP).
