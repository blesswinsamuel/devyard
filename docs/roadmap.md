# Roadmap

What's done, what's planned, and where each item lives in the code.

## Done

- **Core lifecycle.** `start [-f] [--build]`, `stop`, `restart`, `reload`,
  `status`, `logs [-f] [--tail N] [--previous]`, `top`, `kill`, `build`,
  `run`, `attach`. (`internal/cli`, `internal/engine`)
- **Runner-per-process supervision.** Every service, task and terminal runs
  under a detached `devyard --runner`, so services survive daemon restarts
  and crashes and are re-adopted. (`internal/runner`)
- **Actor engine.** Serialized per-entity state machines: no duplicate
  processes, no stuck states, and stop interrupts backoff and dependency
  waits. (`internal/engine`)
- **Readiness-gated dependencies.** Structured `ready` probes (`http`, `tcp`,
  `exec`), a new checker per run, `start_period`; `depends_on` waits until
  ready. (`internal/health`, `internal/engine`)
- **Restart policies.** `never` / `on-failure` (default) / `always`, with
  exponential backoff and jitter. The restart policy also applies to exits
  that happened while the daemon was down.
- **Configurable stop** per service (`stop.signal`, `stop.timeout`).
- **Config.** String-or-argv `run`, layered `env_files`, `port: auto` with
  persisted assignments and `${svc.port}` / `${svc.url}` references,
  `autostart`, `build.sources` fingerprints, `devyard.local.yml` overrides,
  unknown-field warnings, and a generated JSON Schema (`devyard schema`).
  (`internal/config`)
- **Desired state and autostart.** Projects remember running / stopped /
  partial. The daemon adopts live runs and starts only what is wanted.
- **Launch environment capture.** Services run with the environment of the
  shell that started them, not the daemon's.
- **Interactive tasks.** PTY by default. Tasks are detached from the caller
  and attachable from the CLI (`devyard run`) and the web UI.
- **Per-run logs.** Structured records, lossless rotation and following,
  paging, and merged project streams. (`internal/logstore`, `internal/api`)
- **Revisioned state stream.** `Watch` sends a snapshot, then changes, and
  resyncs when a client falls behind. (`internal/events`)
- **Reverse proxy.** Named URLs, TLS (custom certs, mkcert, or a local CA).
  (`internal/proxy`)
- **Web UI.** The embedded SolidJS dashboard. The dashboard is protected by a
  Host allowlist against DNS rebinding, and optionally by a password
  (`devyard auth set-password`, gates the API and terminals behind a login).
  (`internal/web`, `web/`)
- **Git.** Status, log, diff, stage, commit, stash (push/pop/drop),
  push/pull/fetch. Remote operations are bounded by a timeout, and watchers
  handle shared repositories. (`internal/gitlog`, `internal/gitstate`)
- **Project list and live global config.** An ordered `projects` list (and
  `groups`) in the global config, edited by `add`, `project move|remove` and
  the dashboard (drag to reorder) or by hand; projects without a devyard.yml;
  a watched config that rebinds the web and proxy listeners and reports what it
  could not apply. (`internal/projects`, `internal/globalconfig`,
  `internal/daemon`)
- **Config drift.** Config files are watched; changes show as a pending diff
  (dashboard banner, `devyard diff`) and apply on request, automatically or
  never per `reload` policy; invalid files never apply. (`internal/engine`)
- **Hermetic e2e suites and CI** on Ubuntu and macOS. (`test/e2e`,
  `.github/workflows`)

## Planned

- [ ] **Shell completions** (cobra `__complete`).
- [ ] **Prebuilt release binaries** (GoReleaser).
- [ ] **`logs --since`** time filter.
- [ ] **Log search across runs** in the web UI.

## Non-goals

- **Docker Compose compatibility.** We will not add `image`, `volumes`,
  `networks`, or compose-style shims. See [config-schema.md](config-schema.md)
  > "Not part of the schema".
- **Windows support.** Supervision relies on Unix process groups, sessions,
  PTYs, signals and Unix domain sockets.
