# Config schema reference

The shape of `devyard.yml`, with the exact validation rules enforced by
`internal/config/config.go`. Update this doc whenever you change the schema.

For the user-level global config (`web.host`, `web.port`), see
[Global config](#global-config) at the bottom of this doc.

## Top level

```yaml
version: "1"          # required
name: myapp           # optional; defaults to the config file's directory name
services:             # required, non-empty
  <name>: <Service>
tasks:                # optional
  <name>: <Task>
```

- `version` **must** be present (any value is accepted today; the field gates
  future migrations).
- `name` defaults to `filepath.Base(filepath.Dir(configPath))` when omitted.
  The project **id** is the name as a slug (lowercase letters, digits, `-`,
  `_`; e.g. `My App` → `my-app`). Two different config files that resolve to
  the same id conflict: the second `devyard start` fails until you set a
  distinct `name:`.
- `services` must have at least one entry.

### Launch environment

Services run with the environment of the shell that last ran `devyard start`
(or `reload`, or `project add`) for the project. The daemon records it with the
project (mode 0600), so they see the same `PATH`, version managers and tool
settings as your terminal, and daemon restarts or autostart don't change it.
`PWD`, `OLDPWD`, `SHLVL` and `_` are dropped. Projects added from the web UI
use the daemon's environment until you `devyard reload` them from a shell.

### Env files (`--env-file`, `.env`)

A docker-compose-style env file supplies variables for two purposes:

1. **Config interpolation** — `devyard.yml` may reference
   `${VAR}` / `${VAR:-default}` anywhere in its text (command, env values,
   working_dir, ...). References are expanded from the env file variables
   overlaid by the launch environment (the launch environment wins for
   duplicate keys) before the file is parsed.
2. **Child process environment** — every process gets the launch environment,
   overlaid with the env file variables, overlaid with its own `env` (later
   layers win).

The env file choice (`--env-file`) is remembered with the project, so reloads
and autostart use the same file.

The env file is located by default at `.env` next to the config file (optional;
a missing default `.env` is not an error). Override it with
`devyard --env-file <path>` — an explicit path must exist.

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
  restart: always                   # no | on-failure | always
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
| `port` | no | — | The port the process listens on. Shorthand for a single unnamed `ports` entry. Exposes the service to the daemon's reverse proxy (see below). Not Docker's `ports:` mapping — there is no host↔container forwarding. |
| `ports` | no | — | Ordered map of named ports (`http: 3000`). The **first entry is the service's default port**. |
| `proxy` | no | — | Proxy options: `host` overrides the service's host label (default: service name). |
| `tty` | no | `false` | Run in a pseudo-terminal (colors, progress bars, and `devyard attach`). |
| `stop_grace_period` | no | `10s` | Time between SIGTERM and SIGKILL when stopping. |

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

A dependency that is stopped is waited for (the dependent shows `waiting for
<dep>`); starting a service also starts its whole `depends_on` chain.

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
| `start_period` | no | `0` | Grace period after start during which failing probes don't count (a success ends it early). Use it for services that take a while to boot. |

The checker is recreated for every run, so a restarted service starts over at
`starting` (a crashed service is never reported healthy).

The probe inherits the service's `shell`, `working_dir`, and `env`, so a
`CMD-SHELL` probe runs in the same context as the service. State machine:
`starting -> healthy | unhealthy`.

### `restart`

| Policy | Behavior |
| --- | --- |
| `no` (default) | Never restart. |
| `on-failure` | Restart on non-zero exit; gives up after 10 consecutive quick failures (status `failed`). |
| `always` | Restart after any exit. |

Backoff is exponential with jitter, from 0.5s up to 30s, and resets after a run
that lasted 10s. `stop`, `restart` and `start` interrupt a pending backoff
immediately. A killed service (`devyard kill`) stays down.

**Desired state.** Each project records what you want running:

- `devyard start` → the whole project (`running`).
- `devyard start <svc>` on a stopped project → that service and its
  `depends_on` chain (`partial`).
- `devyard stop` → nothing (`stopped`).

When the daemon starts it adopts processes that are still running (they are
never restarted), starts what the project wants running, and leaves stopped
projects stopped. A service that exited on its own while the daemon was down
is handled by its restart policy, exactly as if the daemon had seen it exit.

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

- Runs once before the service starts on `start`.
- `devyard build [service...]` runs the build step for the named services
  (or all, in start order, when none are named). Services without a `build`
  are skipped.
- `devyard start --build` forces a rebuild before starting.
- A failed build aborts `start`/`start --build` so services never start on top of a
  broken build.
- `command` is required in the object form (validation rejects an empty
  command). `env` is additive over the parent env, same rule as service `env`.

## Named URLs (reverse proxy)

Services that declare `port` or `ports` are automatically exposed by the
daemon's built-in reverse proxy at **named URLs** — no port numbers to
remember:

```yaml
version: "1"
name: myproject
proxy:
  default_service: web        # <project>.<domain> routes to this service
services:
  web:
    command: bun run dev
    port: 3000                # → http://web.myproject.localhost:8080
  api:
    command: ./api
    ports:
      http: 3000              # first entry = api's default port
      metrics: 9100           # → metrics.api.myproject.localhost
  db:
    command: postgres -D ...
    # no port/ports → not exposed through the proxy
```

Resulting URLs (default settings):

| URL | Routes to |
| --- | --- |
| `web.myproject.localhost:8080` | web's default port (3000) |
| `myproject.localhost:8080` | web (the `default_service`) |
| `http.api.myproject.localhost:8080` | api's default port (3000) |
| `metrics.api.myproject.localhost:8080` | api port `metrics` (9100) |

Rules:

- A service is routed **only** if it declares `port` or `ports`. `port: N` is
  shorthand for `ports: {<unnamed>: N}`; the two are mutually exclusive.
- Hostnames are `<service>.<project>.<domain>`; a named port prefixes the
  service label (`<port>.<service>.<project>.<domain>`). A `proxy.host` on
  the service replaces the `<service>` label. The project's
  `proxy.default_service` additionally serves `<project>.<domain>`.
- Requests only forward while the service's supervisor reports it running.
  Stopped/starting services get a styled 503 page; an unreachable upstream
  gets a 502 page.
- The proxy forwards to `127.0.0.1:<port>`, preserves the original `Host`
  header, and passes WebSocket upgrades through.
- Validation: `port` and `ports` are mutually exclusive; port names and
  `proxy.host` must be lowercase letters, digits, and dashes; an unknown or
  portless `proxy.default_service` is a config error.

**LAN access.** `*.localhost` resolves to 127.0.0.1 on the machine itself only
(RFC 6761). To reach the proxy from other machines, set `proxy.host: 0.0.0.0`
in the global config and a `proxy.domain_suffix` that resolves to the host's
IP on your LAN — e.g. `192-168-1-5.nip.io` (zero setup via nip.io/sslip.io) or
a wildcard DNS zone. Note the proxy is unauthenticated; binding to a
non-loopback address exposes your dev services to the network.

## Tasks

Tasks define one-off, task-oriented commands (e.g. `db:migrate`, `seed`, `test`, `build`) that are executed on demand via `devyard task run <task>` (or shortcut `devyard run <task>`) or the Web UI.

```yaml
tasks:
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
| `command` | yes | — | Command to run. Can be extended via CLI args (`devyard run <task> -- <args>`). |
| `working_dir` | no | config dir | Relative path resolved against config file directory. |
| `env` | no | — | Map of environment variables for the task process. |
| `shell` | no | `sh` | Shell used to run command. |
| `depends_on` | no | — | Dependent services auto-started and waited for before running the task. |
| `tty` | no | `true` | Run in a pseudo-terminal so interactive prompts work. Set `false` for plain piped output (stdin is still available through `devyard run` / the web UI). |

Task runs belong to the daemon: closing the browser tab or losing the CLI
connection does not stop them, and they survive daemon restarts. `devyard
run` attaches your terminal (keystrokes are forwarded; Ctrl-C stops the
task). Stopping the project stops running tasks.

## Intentionally absent

`image`, `build:` (Docker context), `volumes`, and `networks` are **not** part
of the schema. Processes bind ports and read the filesystem directly —
there's nothing to map. Adding shims for these would mislead users about what
`devyard` does.

Note: the service-level `port`/`ports` fields described above are **not**
Docker's `ports:` mapping (host↔container forwarding) — they declare the
port(s) a process listens on so the reverse proxy can route named URLs to
them. There is still no port forwarding of any kind.

## Unknown fields

`yaml.v3` ignores unknown fields by default, so a typo like `comand:` is
silently dropped — be careful when editing configs. (A warning pass is on the
roadmap.)

## Global config

In addition to the per-project `devyard.yml`, the daemon reads a
user-level global config at `$XDG_CONFIG_HOME/devyard/config.yml`
(default `~/.config/devyard/config.yml`) for default bind settings:

```yaml
web:
  host: 127.0.0.1      # bind address (default: 127.0.0.1, loopback only)
  port: 9090           # TCP port (default: 9090; 0 = pick a free port)
  allowed_hosts: []    # extra Host headers the dashboard accepts
proxy:
  host: 127.0.0.1      # bind address (default: 127.0.0.1, loopback only)
  port: 8080           # TCP port (default: 8080)
  domain_suffix: localhost
  tls:
    enabled: false     # enable HTTPS (default: false)
    port: 8443         # HTTPS TCP port (default: 8443, or 443 if proxy.port is 80)
    cert_file: ""      # custom PEM cert file (optional; defaults to auto-generated local CA / mkcert)
    key_file: ""       # custom PEM private key file (optional)
    http_redirect: false # redirect HTTP requests to HTTPS (default: false)
```

| Field | Default | Notes |
| --- | --- | --- |
| `web.host` | `127.0.0.1` | Bind address for the daemon's web dashboard. Set to `0.0.0.0` for remote access. |
| `web.port` | `9090` | TCP port for the web UI. `0` picks a free port (see `devyard daemon status`). |
| `web.allowed_hosts` | `[]` | Extra hostnames the dashboard answers to (`*.example.com` matches subdomains). Loopback names, IP addresses, `devyard` and names under `proxy.domain_suffix` are always allowed; anything else gets 403, which blocks DNS-rebinding attacks. |
| `proxy.host` | `127.0.0.1` | Bind address for the daemon's reverse proxy. Set to `0.0.0.0` for LAN access. |
| `proxy.port` | `8080` | TCP port for the reverse proxy. `0` picks a free port. |
| `proxy.domain_suffix` | `localhost` | Domain routes are served under. Set to a nip.io name (e.g. `192-168-1-5.nip.io`) or a wildcard DNS zone for LAN access. |
| `proxy.tls.enabled` | `false` | Enable TLS/HTTPS on the reverse proxy. |
| `proxy.tls.port` | `8443` | HTTPS port (defaults to `8443`, or `443` if `proxy.port` is `80`). |
| `proxy.tls.cert_file` | `""` | Path to custom certificate PEM file. When omitted, uses `mkcert` CA if found, or generates a local devyard Root CA. |
| `proxy.tls.key_file` | `""` | Path to custom private key PEM file. |
| `proxy.tls.http_redirect` | `false` | When true, incoming HTTP requests redirect (307) to HTTPS. |

Listener changes take effect after `devyard daemon restart` (services keep
running). `allowed_hosts` applies immediately when saved from the web UI.

Unknown fields in the global config produce a warning (printed to stderr) but
do not error, matching the convention for `devyard.yml`.

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
      env: { NODE_ENV: production }
      shell: bash
  db:
    command: postgres -D /usr/local/var/postgres
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 5s
      retries: 5
```
