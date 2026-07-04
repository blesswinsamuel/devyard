# Roadmap

What's done, what's planned, and where each item lives in the code.

## Done

- **Core lifecycle** — `up`, `up -d` (detached supervisor via `setsid` re-exec),
  `down`, `ps`, `logs [service] [--follow]`, `restart [service]`,
  `build [service...]`, `up --build`. (`internal/cli`, `internal/supervisor`,
  `internal/daemon`, `internal/control`)
- **Process-group safety** — each service in its own `setpgid` group; teardown
  via `killpg` so no orphans. (`internal/supervisor/proc_unix.go`)
- **Dependency ordering** — `depends_on` graph with cycle detection and
  topological start order. (`internal/dag`)
- **Healthchecks + conditions** — per-service `starting -> healthy | unhealthy`
  state machine; `depends_on: { condition: service_healthy }` gates dependents.
  (`internal/health`, `internal/supervisor`)
- **Restart policies** — `no` / `on-failure` / `always` / `unless-stopped` with
  exponential backoff + jitter; `unless-stopped` persists a stopped marker so
  it doesn't auto-resume. (`internal/supervisor/restart.go`)
- **TUI** — Bubble Tea v2 frontend: service list, streaming logs, restart/stop/
  down keybindings, "offer to start" when no supervisor is running.
  (`internal/tui`)
- **Config discovery** — `-f`/`-p` flags; walk-up discovery of
  `local-compose.yml`. (`internal/config`, `internal/cli`)
- **Tests** — unit tests per package plus a black-box integration suite that
  builds the real binary and drives the full lifecycle. (`test/integration`)

## Planned

- [ ] **`local-compose web`** — a browser dashboard (HTTP + WebSocket) over the
      same control socket protocol. Loopback-only by default; assets embedded
      via `embed.FS` so the binary stays single-file. Status: stub
      (`internal/web/doc.go` only). See
      [architecture.md](architecture.md) and [control-protocol.md](control-protocol.md).
- [ ] **`.env` / `--env-file` loading** and `${VAR}` interpolation in config.
- [ ] **Log rotation** by size in the state dir; `logs --tail N` / `--since`.
- [ ] **Configurable graceful stop timeout** per service / via flag (currently
      fixed at 10s in `Supervisor`).
- [ ] **Shell completions** (cobra `__complete`) and `local-compose version`.
- [ ] **Strict config mode** that warns on unknown fields (today `yaml.v3`
      silently ignores them).
- [ ] **Prebuilt release binaries** (GoReleaser) + CI workflow
      (`go vet`, `gofmt`, `golangci-lint`, `go test ./...`).

## Non-goals

- Docker compatibility — we will not add `image`, `volumes`, `networks`, or
  `ports` shims. See [config-schema.md](config-schema.md) > "Intentionally
  absent".
- Windows support — the supervisor relies on Unix process groups, signals, and
  a Unix domain socket. A Windows port would need a separate supervisor
  implementation (Job Objects) and transport (named pipes/TCP).
