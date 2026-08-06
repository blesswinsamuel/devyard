# Config schema reference

The shape of `local-compose.yml`, with the exact validation rules enforced by
`internal/config/config.go`. Update this doc whenever you change the schema.

For the user-level global config (`web.host`, `web.port`), see
[Global config](#global-config) at the bottom of this doc.

## Top level

```yaml
version: "1"          # required
name: myapp           # optional; defaults to the config file's directory name
services:             # required, non-empty
  <name>: <Service>
actions:              # optional
  <name>: <Action>
```

- `version` **must** be present (any value is accepted today; the field gates
  future migrations).
- `name` defaults to `filepath.Base(filepath.Dir(configPath))` when omitted.
- `services` must have at least one entry.

### Env files (`--env-file`, `.env`)

A docker-compose-style env file supplies variables for two purposes:

1. **Config interpolation** — `local-compose.yml` may reference
   `${VAR}` / `${VAR:-default}` anywhere in its text (command, env values,
   working_dir, ...). References are expanded from the env file variables
   overlaid on the process environment (the process environment wins for
   duplicate keys) before the file is parsed.
2. **Child process environment** — the env file variables are passed to every
   service process, layered under the service's own `env` (service `env` wins)
   and under the parent environment.

The env file is located by default at `.env` next to the config file (optional;
a missing default `.env` is not an error). Override it with
`local-compose --env-file <path>` — an explicit path must exist.

Supported env file syntax: `KEY=VALUE` lines, blank lines, `#` comments,
optional `export ` prefix, and single/double-quoted values.

Interpolation forms:

| Form | Meaning |
| --- | --- |
| `${VAR}` | Value of `VAR`; empty + a warning when unset |
| `${VAR:-def}` | `def` when `VAR` is unset or empty |
| `${VAR-def}` | `def` when `VAR` is unset |
| `$$` | Literal `$` |

Bare `$VAR` references (no braces) are left untouched, so shell-style
`$HOME` inside `command` still reaches the shell.

> Example: `PORT=${PORT:-8080}` in a service `command` becomes `8080` when the
> env file (or process env) does not define `PORT`.

## Service

```yaml
api:
  command: cargo run --bin api      # required
  working_dir: ./api                # optional, relative to the config file
  env:                              # optional, additive over the parent env
    DATABASE_URL: postgres://localhost/myapp
  shell: sh                         # optional, default "sh"
  depends_on: [db]                  # list OR map (see below)
  healthcheck:                      # optional
    test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
    interval: 5s
    timeout: 2s
    retries: 10
  restart: unless-stopped           # no | on-failure | always | unless-stopped
  build: cargo build --bin api      # string OR object (see below)
```

| Field | Required | Default | Notes |
| --- | :-: | --- | --- |
| `command` | yes | — | Run via `<shell> -c <command>`, so pipelines work. |
| `working_dir` | no | config dir | Relative paths resolve against the config file's directory. |
| `env` | no | — | Map of string→string. **Additive** over the parent process env (parent keys not in `env` are preserved). Empty keys are rejected. |
| `shell` | no | `sh` | Shell used to run `command`. |
| `depends_on` | no | — | List of names or map of `name: { condition: ... }`. |
| `healthcheck` | no | — | See below. |
| `restart` | no | `no` | See below. |
| `build` | no | — | Pre-start build step; string or object. |

### `depends_on`

Two forms, deserialized by a custom `UnmarshalYAML`:

```yaml
depends_on: [api, db]                       # list: condition defaults to service_started
```

```yaml
depends_on:                                 # map: explicit conditions
  db: { condition: service_healthy }
  api: { condition: service_started }
```

Conditions:

| Condition | Meaning |
| --- | --- |
| `service_started` (default) | Dependent starts once the dependency has launched at least once. |
| `service_healthy` | Dependent waits until the dependency's healthcheck is `healthy`. Fails fast if it goes `unhealthy` or exits first. |

Validation:

- Every name referenced must exist in `services`.
- `service_healthy` requires the referenced service to declare a
  `healthcheck` (config validation rejects it otherwise).
- Cycles are detected at DAG build time (`internal/dag`), not in `config`.

### `healthcheck`

```yaml
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
  interval: 5s
  timeout: 2s
  retries: 10
```

| Field | Required | Default | Notes |
| --- | :-: | --- | --- |
| `test` | yes | — | `["CMD", "exe", "args..."]` (exec'd directly) or `["CMD-SHELL", "command"]` (run via `shell -c`). Must start with `CMD` or `CMD-SHELL` and have at least one more element. |
| `interval` | no | `5s` | Time between probes. Go duration string. |
| `timeout` | no | `2s` | Per-probe timeout; the probe process group is killed after this. |
| `retries` | no | `3` | Consecutive failures required to flip to `unhealthy`. One success recovers to `healthy` and resets the counter. |

The probe inherits the service's `shell`, `working_dir`, and `env`, so a
`CMD-SHELL` probe runs in the same context as the service. State machine:
`starting -> healthy | unhealthy`.

### `restart`

| Policy | Behavior |
| --- | --- |
| `no` (default) | Never restart. |
| `on-failure` | Restart on non-zero exit, up to `Backoff.MaxAttempts` (capped). |
| `always` | Restart forever, any exit. |
| `unless-stopped` | Restart forever, **but** an explicit `stop`/`down` writes a "stopped" marker so the service does not auto-resume on the next **daemon autostart**. Explicit `up`/`start` (all services) clears markers and starts the service; `start <svc>` / `restart` also clear it. When a service is skipped at autostart this way, its dependents are skipped transitively (logged as `skipped: dependency "X" is stopped`) and startup exits 0 — an explicit stop is not a failure. |

Note: a **project-level** `.stopped` marker (written by `down` / `stop` with no service) suppresses **daemon autostart** for the whole project, including services with `restart: always`. Explicit `up`/`start` clears it. Stopped projects remain listed in `ls` / the web UI.

Backoff is exponential with jitter (`internal/supervisor` `BackoffConfig`).

### `build`

Two forms, deserialized by a custom `UnmarshalYAML`:

```yaml
build: cargo build --bin api                 # string shorthand
```

```yaml
build:                                       # object form
  command: pnpm build                        # required
  working_dir: ./web                         # optional, relative to config file
  env:                                       # optional, additive over parent env
    NODE_ENV: production
  shell: bash                                # optional, default "sh"
```

- Runs once before the service starts on `up`.
- `local-compose build [service...]` runs the build step for the named services
  (or all, in start order, when none are named). Services without a `build`
  are skipped.
- `up --build` forces a rebuild before starting.
- A failed build aborts `up`/`up --build` so services never start on top of a
  broken build.
- `command` is required in the object form (validation rejects an empty
  command). `env` is additive over the parent env, same rule as service `env`.

## Actions

Actions define one-off, task-oriented commands (e.g. `db:migrate`, `seed`, `test`, `build`) that are executed on demand via `local-compose run <action>` or the Web UI.

```yaml
actions:
  # Short form (string command)
  migrate: npx prisma db push

  # Long form (object specification)
  seed:
    command: node scripts/seed.js
    working_dir: ./backend
    env:
      NODE_ENV: development
    shell: bash
    depends_on:
      db: { condition: service_healthy }
```

| Field | Required | Default | Notes |
| --- | :-: | --- | --- |
| `command` | yes | — | Command to run. Can be extended via CLI args (`local-compose run <action> -- <args>`). |
| `working_dir` | no | config dir | Relative path resolved against config file directory. |
| `env` | no | — | Map of environment variables for the action process. |
| `shell` | no | `sh` | Shell used to run command. |
| `depends_on` | no | — | Dependent services auto-started and waited for before running the action. |

## Intentionally absent

`image`, `build:` (Docker context), `volumes`, `networks`, and `ports` are
**not** part of the schema. Processes bind ports and read the filesystem
directly — there's nothing to map. Adding shims for these would mislead users
about what `local-compose` does.

## Unknown fields

`yaml.v3` ignores unknown fields by default. We don't add strict-mode
decoding, so a typo like `comand:` is silently dropped — be careful when
editing configs. (A future strict mode + warning pass is on the roadmap.)

## Global config

In addition to the per-project `local-compose.yml`, `local-compose web` reads a
user-level global config at `$XDG_CONFIG_HOME/local-compose/config.yml`
(default `~/.config/local-compose/config.yml`) for default bind settings:

```yaml
web:
  host: 127.0.0.1      # bind address (default: 127.0.0.1, loopback only)
  port: 9090           # TCP port (default: 9090)
```

| Field | Default | Notes |
| --- | --- | --- |
| `web.host` | `127.0.0.1` | Bind address for `local-compose web`. Set to `0.0.0.0` for remote access. |
| `web.port` | `9090` | TCP port for the web UI. |

CLI flags `--host` / `--port` override these defaults.

Unknown fields in the global config produce a warning (printed to stderr) but
do not error, matching the convention for `local-compose.yml`.

Implementation: `internal/globalconfig/globalconfig.go`. Tests:
`internal/globalconfig/globalconfig_test.go`.

## Example

```yaml
version: "1"
name: myapp
services:
  api:
    command: cargo run --bin api
    working_dir: ./api
    build: cargo build --bin api
    depends_on:
      db: { condition: service_healthy }
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
      env: { NODE_ENV: production }
      shell: bash
  db:
    command: postgres -D /usr/local/var/postgres
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 5s
      retries: 5
```
