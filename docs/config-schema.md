# Config schema reference

The shape of `local-compose.yml`, with the exact validation rules enforced by
`internal/config/config.go`. Update this doc whenever you change the schema.

## Top level

```yaml
version: "1"          # required
name: myapp           # optional; defaults to the config file's directory name
services:             # required, non-empty
  <name>: <Service>
```

- `version` **must** be present (any value is accepted today; the field gates
  future migrations).
- `name` defaults to `filepath.Base(filepath.Dir(configPath))` when omitted.
- `services` must have at least one entry.

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
| `unless-stopped` | Restart forever, **but** an explicit `stop`/`down` writes a "stopped" marker so the service does not auto-resume on the next `up`. `restart` clears the marker. |

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

## Intentionally absent

`image`, `build:` (Docker context), `volumes`, `networks`, and `ports` are
**not** part of the schema. Processes bind ports and read the filesystem
directly — there's nothing to map. Adding shims for these would mislead users
about what `local-compose` does.

## Unknown fields

`yaml.v3` ignores unknown fields by default. We don't add strict-mode
decoding, so a typo like `comand:` is silently dropped — be careful when
editing configs. (A future strict mode + warning pass is on the roadmap.)

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
