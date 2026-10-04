# Config schema reference

The shape of `devyard.yml`, with the exact rules enforced by
`internal/config`. Update this doc, the tests in `internal/config`, and the
generated JSON Schema (`go generate ./internal/config`) whenever you change
the schema.

For the user-level global config (`web`, `proxy`), see
[Global config](#global-config) at the bottom of this doc.

## Example

```yaml
# yaml-language-server: $schema=https://…/devyard.schema.json
name: myapp                    # optional; default = directory name
primary: web                   # served at myapp.localhost
env_files: [.env, .env.local]  # the default
env: { LOG_LEVEL: debug }      # every service and task
links:
  Grafana: http://localhost:3001

services:
  db:
    run: postgres -D .data/pg
    port: 5432
    ready: { tcp: {} }
    stop: { signal: SIGINT }

  api:
    run: [cargo, run, --bin, api]    # a list is executed directly
    dir: ./api
    port: auto                       # devyard picks a free port → $PORT
    depends_on: [db]                 # waits until db is ready
    env: { DATABASE_URL: "postgres://localhost:${db.port}/myapp" }
    ready:
      http: { path: /health }
    build:
      run: cargo build --bin api
      sources: [api/src/**, Cargo.lock]

  web:
    run: bun run dev --port $PORT
    dir: ./web
    port: auto
    env: { API_URL: "${api.url}" }
    tty: true
    depends_on: [api]

  storybook:
    run: bun storybook --port $PORT
    port: auto
    autostart: false                 # only when named

tasks:
  migrate: sqlx migrate run
  seed:
    run: node scripts/seed.js
    depends_on: [db]
```

## Editor support

`devyard schema` prints a JSON Schema for the file (generated from the Go
types; the source lives at `internal/config/devyard.schema.json`). Editors
using the YAML language server pick it up from a modeline:

```yaml
# yaml-language-server: $schema=/path/to/devyard.schema.json
```

It gives completion, inline docs, and flags unknown fields.

## Top level

| Field | Default | Notes |
| --- | --- | --- |
| `name` | directory name | The project id is the name as a slug (lowercase letters, digits, `-`, `_`; `My App` → `my-app`). Two configs that resolve to the same id conflict: the second `devyard start` fails until you set a distinct `name`. |
| `primary` | — | Service served at `<project>.<domain>` and opened by the dashboard. Must have a port. |
| `env_files` | `[.env, .env.local]` | Env files, relative to the config's directory, loaded in order (later files win). Missing files are skipped. `env_files: []` loads none. |
| `env` | — | Variables for every service and task. |
| `links` | — | Ordered map of name → absolute URL, shown on the project page. |
| `services` | — | Map of name → [service](#services). |
| `tasks` | — | Map of name → [task](#tasks). |

Every field is optional. An empty `devyard.yml` is a valid project (useful for
the git UI and terminals alone).

Unknown fields produce a warning with their path and line (`unknown field
"comand" at services.api.comand (line 7)`), not an error.

## Commands: `run`

`run` is either a string or a list:

```yaml
run: bun run dev --port $PORT        # runs with: sh -c '<string>'
run: [cargo, run, --bin, api]        # executed directly, no shell
```

A string gets shell features (pipes, `&&`, `$VAR`). There is no `shell`
setting: for another shell, use the list form
(`run: [bash, -c, "source venv/bin/activate && exec uvicorn app:app"]`) or a
script file.

Services, tasks and `build` also accept the command alone:

```yaml
services:
  api: ./bin/api                     # same as { run: ./bin/api }
tasks:
  lint: [bun, run, lint]
```

## Services

| Field | Default | Notes |
| --- | --- | --- |
| `run` | **required** | See [Commands](#commands-run). |
| `dir` | config dir | Working directory, relative to the config's directory. |
| `env` | — | Variables for this service. |
| `env_files` | — | Env files for this service, loaded after the project's. Missing files are skipped. |
| `depends_on` | — | Services to wait for. See [Dependencies](#dependencies). |
| `ready` | — | [Readiness probe](#readiness-ready). |
| `restart` | `on-failure` | `never`, `on-failure` or `always`. See [Restarts](#restarts). |
| `build` | — | [Build step](#builds). |
| `tty` | `false` | Run in a pseudo-terminal (colors, progress bars, `devyard attach`). |
| `port` | — | Port the service listens on: a number or `auto`. |
| `ports` | — | Ordered map of named ports (`http: 3000`, `metrics: auto`); the first is the default port. Mutually exclusive with `port`. |
| `host` | service name | Host label in the proxy URL. Lowercase letters, digits and dashes. |
| `stop.signal` | `SIGTERM` | Signal sent to stop the service (`SIGINT`, `INT`, `2`, …). |
| `stop.timeout` | `10s` | Wait after the signal before `SIGKILL`. |
| `autostart` | `true` | When `false`, the service starts only when named (`devyard start storybook`) or needed by a service that starts. |

Service names must be lowercase letters, digits and dashes: they appear in
hostnames and `${service.port}` references.

### Dependencies

```yaml
depends_on: [db, cache]
```

A service starts once each dependency is **ready**:

- running, and healthy if it has a `ready` probe; or
- exited with code 0 (a one-shot setup service).

A dependency that is stopped is started along with the dependent and waited
for (the dependent shows `waiting for db`). One that is unhealthy is waited
for, since it may recover. One that exits non-zero or fails fails the
dependent.

Starting a service starts its whole `depends_on` chain. Cycles are rejected.

### Readiness: `ready`

A probe that tells devyard (and dependents) when the service is ready.
Exactly one of `http`, `tcp` and `exec`:

```yaml
ready:
  http: { path: /health }            # GET http://127.0.0.1:<port>/health → 2xx/3xx
ready:
  http: { path: /ping, port: admin, status: 204 }
ready:
  tcp: {}                            # connect to 127.0.0.1:<port>
ready:
  tcp: { port: 5432 }
ready:
  exec: pg_isready -q                # exit code 0; string or list
```

| Field | Default | Notes |
| --- | --- | --- |
| `http.path` | `/` | Must start with `/`. Redirects are not followed. |
| `http.port`, `tcp.port` | default port | A port name of the service or a number. |
| `http.status` | any 2xx or 3xx | Expected status code. |
| `exec` | — | Runs in the service's directory and environment, in its own process group (killed on timeout). |
| `interval` | `2s` | Time between probes. |
| `timeout` | `2s` | Time limit for one probe. |
| `retries` | `30` | Consecutive failures that mark the service `unhealthy`. |
| `start_period` | `0` | Grace period after start during which failures don't count. A success ends it early. |

The first probe runs as soon as the process starts. States:
`starting → healthy | unhealthy`; a success recovers `unhealthy` to
`healthy`. Each run gets a fresh probe, so a restarted service starts over
at `starting`.

### Restarts

| Policy | Behavior |
| --- | --- |
| `never` | Never restart. |
| `on-failure` (default) | Restart after a non-zero exit; give up after 10 consecutive quick failures (status `failed`). |
| `always` | Restart after any exit. |

Backoff is exponential with jitter, from 0.5s up to 30s, and resets after a
run that lasted 10s. `stop`, `restart` and `start` interrupt a pending
backoff. A killed service (`devyard kill`) stays down. The restart count is
shown next to the service in the dashboard and in `devyard status`; it resets
on an explicit start or restart.

**Desired state.** Each project records what you want running:

- `devyard start` → the project's autostart services (`running`).
- `devyard start <svc>` → that service and its `depends_on` chain, in
  addition to whatever already runs (`partial` on a stopped project).
- `devyard stop` → nothing (`stopped`).

When the daemon starts it adopts processes that are still running (they are
never restarted), starts what the project wants running, and leaves stopped
projects stopped. A service that exited while the daemon was down is handled
by its restart policy, as if the daemon had seen it exit.

### Builds

```yaml
build: cargo build --bin api         # shorthand
build:
  run: pnpm build
  dir: ./web                         # default: the service's dir
  env: { NODE_ENV: production }      # added to the service's environment
  sources: [web/src/**, web/package.json]
```

- `devyard start --build` (and `devyard build`) run the build before
  starting.
- With `sources`, a plain start also builds when the matched files changed
  since the last successful build (by path, size and modification time). List
  inputs, not outputs. Patterns are relative to the config's directory; `**`
  matches any number of directories.
- A failed build aborts the start, so a service never runs on a broken build.
- The build runs with the service's environment (including `$PORT`) plus
  `build.env`.

## Ports and URLs

```yaml
services:
  web:
    run: bun run dev --port $PORT
    port: auto
  api:
    run: ./api
    ports:
      http: 8080
      metrics: auto
```

- **`auto`** picks a free port the first time the service is resolved and
  keeps it (per project, in the state directory) across restarts and daemon
  restarts. Removing the port releases it.
- Every service with ports gets `PORT` (the default port) and `PORT_<NAME>`
  for each named port (`metrics-v2` → `PORT_METRICS_V2`). The service's own
  `env` can override them.
- **References**: `${api.port}`, `${api.ports.metrics}` and `${api.url}`
  (`http://127.0.0.1:<default port>`) expand in `run`, `env`, `build` and
  `ready.exec` of any service or task. Referencing an unknown service or port
  is a config error.

### Reverse proxy

Services with a port are exposed by the daemon's reverse proxy at named URLs:

| URL | Routes to |
| --- | --- |
| `web.myapp.localhost:8080` | web's default port |
| `myapp.localhost:8080` | the `primary` service |
| `metrics.api.myapp.localhost:8080` | api's `metrics` port |

- Hostnames are `<host>.<project>.<domain>`, where `host` defaults to the
  service name; a named port prefixes it (`<port>.<host>.<project>.<domain>`).
- Requests forward only while the service is running. Stopped or starting
  services get a styled 503 page; an unreachable upstream gets a 502 page.
- The proxy forwards to `127.0.0.1:<port>`, preserves the `Host` header, and
  passes WebSocket upgrades through.

**LAN access.** `*.localhost` resolves to 127.0.0.1 on the machine itself only
(RFC 6761). To reach the proxy from other machines, set `proxy.host: 0.0.0.0`
in the global config and a `proxy.domain_suffix` that resolves to the host's
IP on your LAN — e.g. `192-168-1-5.nip.io` (zero setup via nip.io/sslip.io) or
a wildcard DNS zone. The proxy is unauthenticated; binding to a non-loopback
address exposes your dev services to the network.

## Tasks

On-demand commands, run with `devyard run <task> [args...]` or from the web
UI.

```yaml
tasks:
  migrate: npx prisma db push
  seed:
    run: node scripts/seed.js
    dir: ./backend
    env: { NODE_ENV: development }
    depends_on: [db]
```

| Field | Default | Notes |
| --- | --- | --- |
| `run` | **required** | Extra arguments are appended (shell-quoted for a string `run`). |
| `dir` | config dir | Relative to the config's directory. |
| `env` | — | Variables for this task. |
| `env_files` | — | Loaded after the project's env files. |
| `depends_on` | — | Services started and waited for (until ready) before the task runs. |
| `tty` | `true` | Run in a pseudo-terminal so prompts, colors and Ctrl-C work. Set `false` for separate stdout/stderr in the logs. |

Task runs belong to the daemon: closing the browser tab or losing the CLI
connection does not stop them, and they survive daemon restarts. `devyard
run` attaches your terminal (keystrokes are forwarded; Ctrl-C stops the
task). Stopping the project stops running tasks.

## Environment

Every process gets, later layers winning:

1. the **launch environment**: the environment of the shell that last ran
   `devyard start` (or `reload`, or `add`), minus `PWD`, `OLDPWD`,
   `SHLVL` and `_`. The daemon stores it with the project (mode 0600), so
   services see the same `PATH`, version managers and tool settings as your
   terminal, regardless of daemon restarts or autostart. Projects added from
   the web UI use the daemon's environment until you `devyard reload` them
   from a shell;
2. the project `env_files`;
3. the project `env`;
4. the service's or task's `env_files`;
5. `PORT` / `PORT_<NAME>` (services);
6. the service's or task's `env`.

The dashboard lists which variables the project defines (names only; values
never leave the daemon).

### Interpolation

`${VAR}` references in any value are expanded when the file is loaded,
from the project env files overlaid by the launch environment (the launch
environment wins):

| Form | Meaning |
| --- | --- |
| `${VAR}` | Value of `VAR`; empty and a warning when unset |
| `${VAR:-def}` | `def` when `VAR` is unset or empty |
| `${VAR-def}` | `def` when `VAR` is unset |
| `$$` | Literal `$` |

Bare `$VAR` (no braces) is left alone, so `$HOME` or `$PORT` in a string
`run` reaches the shell. Service references (`${api.port}`) are not env
variables; see [Ports and URLs](#ports-and-urls). Values are interpolated
after parsing, so a variable can't inject YAML structure, and
`port: ${API_PORT:-3000}` still parses as a number.

Env file syntax: `KEY=VALUE` lines, blank lines, `#` comments, an optional
`export ` prefix, and single- or double-quoted values.

## Local overrides: `devyard.local.yml`

An optional `devyard.local.yml` next to `devyard.yml` (keep it out of git)
is merged over it: maps merge key by key, everything else (strings, lists)
replaces.

```yaml
# devyard.local.yml
services:
  api:
    env: { RUST_LOG: trace }       # added to api's env
    depends_on: []                 # replaces the list
```

## Not part of the schema

`image`, `volumes`, `networks`, Docker-style `ports:` mappings and
healthcheck `CMD` arrays don't exist: processes bind ports and read the
filesystem directly, and devyard is not a Docker Compose clone. The `port`
and `ports` fields declare what a process listens on so the proxy can route
to it; there is no port forwarding.

## Global config

In addition to the per-project `devyard.yml`, the daemon reads a
user-level global config at `$XDG_CONFIG_HOME/devyard/config.yml`
(default `~/.config/devyard/config.yml`): the list of projects, groups of
them, and the bind settings of the dashboard and the proxy.

```yaml
projects:              # which projects exist, in display order
  - ~/dev/myapp        # a directory (devyard.yml optional)
  - ~/dev/notes
  - ~/dev/legacy/devyard.yaml   # or the path of a config file
groups:
  work: [myapp, api]   # project ids; `devyard start @work`
web:
  host: 127.0.0.1      # bind address (default: 127.0.0.1, loopback only)
  port: 9090           # TCP port (default: 9090; 0 = pick a free port)
  allowed_hosts: []    # extra Host headers the dashboard accepts
  password_hash: ""    # bcrypt hash of the dashboard password (devyard auth set-password)
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

### Projects and groups

`projects` is the single source of truth for which projects exist and in
which order the dashboard and `devyard project list` show them. Each entry is
a directory or the path of a config file; `~` is expanded.

- A **directory** is looked at for `devyard.yml`, then `devyard.yaml`. A
  directory with neither is a project with no services: the dashboard still
  gives it the git view and terminals, and a `devyard.yml` created later is
  picked up without a reload.
- `devyard add [path]`, `devyard start` and `devyard run` in a new directory,
  and the dashboard's Add project dialog append to the list. `devyard project
  remove` and the dashboard's Remove drop an entry, stop the project and
  delete its state and logs; the dashboard offers the last 20 removed projects
  again when adding. `devyard project move` and dragging in the sidebar
  reorder the list.
- These edits change the file in place: your comments and other keys stay.
- Editing the list by hand works too ([live reload](#live-reload)). A missing `projects` key
  changes nothing; an empty list removes every project. A directory that no
  longer exists is shown as a project in an error state, so you can see it and
  remove it.
- The first daemon to start without a `projects` key adopts the projects
  already registered with it into the list.

`groups` maps a name to project ids. `devyard start|stop|restart @name` acts
on every project of the group, in order. Groups are edited in the file only.

### Daemon settings

| Field | Default | Notes |
| --- | --- | --- |
| `web.host` | `127.0.0.1` | Bind address for the daemon's web dashboard. Set to `0.0.0.0` for remote access. |
| `web.port` | `9090` | TCP port for the web UI. `0` picks a free port (see `devyard daemon status`). |
| `web.allowed_hosts` | `[]` | Extra hostnames the dashboard answers to (`*.example.com` matches subdomains). Loopback names, IP addresses, `devyard` and names under `proxy.domain_suffix` are always allowed; anything else gets 403, which blocks DNS-rebinding attacks. |
| `web.password_hash` | `""` | bcrypt hash of the dashboard password (`devyard auth set-password` or the settings UI; never hand-write it). When set, the API, terminals and logs require a login (the SPA shows a login screen); empty leaves the dashboard open. Set it when binding to `0.0.0.0` or exposing the dashboard beyond loopback. Applies without a restart when set through the daemon (`devyard auth` with one running, or the settings UI); a hand-edited file applies at the next start. |
| `proxy.host` | `127.0.0.1` | Bind address for the daemon's reverse proxy. Set to `0.0.0.0` for LAN access. |
| `proxy.port` | `8080` | TCP port for the reverse proxy. `0` picks a free port. |
| `proxy.domain_suffix` | `localhost` | Domain routes are served under. Set to a nip.io name (e.g. `192-168-1-5.nip.io`) or a wildcard DNS zone for LAN access. |
| `proxy.tls.enabled` | `false` | Enable TLS/HTTPS on the reverse proxy. |
| `proxy.tls.port` | `8443` | HTTPS port (defaults to `8443`, or `443` if `proxy.port` is `80`). |
| `proxy.tls.cert_file` | `""` | Path to custom certificate PEM file. When omitted, uses `mkcert` CA if found, or generates a local devyard Root CA. |
| `proxy.tls.key_file` | `""` | Path to custom private key PEM file. |
| `proxy.tls.http_redirect` | `false` | When true, incoming HTTP requests redirect (307) to HTTPS. |

`password_hash` is managed by `devyard auth set-password` / `devyard auth
clear` and the settings UI; saving the global config from the web UI never
touches it.

### Live reload

The daemon watches the file; there is no need to restart it, and services are
never touched by a settings change. About 150 ms after a save (by an editor,
the CLI or the dashboard) it applies every part that changed:

- `projects` and `groups`: projects are added, stopped and removed, and
  reordered, as described above.
- `web.host` / `web.port`: the dashboard moves to the new address. The new
  listener is bound first, so an address that cannot be bound leaves the old
  one serving; the old one is closed a few seconds later. Reload the page at
  the new address.
- `proxy.*`: the reverse proxy is rebuilt (host, port, TLS, certificates,
  `domain_suffix`) and the URLs of services are updated. A bad address leaves
  the old proxy serving.
- `web.allowed_hosts` and `web.password_hash` apply immediately.

A file that does not parse (or has an out-of-range value) changes nothing: the
last good config stays in effect. A part that parses but cannot be applied (a
port in use, a project that cannot be loaded) keeps its old behavior and the
rest is applied. Either way the problem is shown as a banner in the dashboard,
by `devyard daemon status`, and in `config_error` of `GetDaemon`; it clears
when the file is fixed.

Unknown fields in the global config produce a warning (printed to stderr) but
do not error, matching `devyard.yml`.

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
