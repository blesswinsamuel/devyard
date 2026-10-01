# Control protocol

The daemon exposes one ConnectRPC service, `devyard.v1.DaemonService`,
defined in [`proto/devyard/v1/control.proto`](../proto/devyard/v1/control.proto).
Generated code lives in `internal/gen/proto` (Go) and `web/src/gen` (TS).
Regenerate with `buf generate` after editing the proto.

## Transports

| Client | Transport |
|---|---|
| CLI | Unix socket `$XDG_RUNTIME_DIR/devyard/daemon.sock`, unencrypted HTTP/2 (needed for bidi `Attach`). See `internal/client`. |
| Web UI | The dashboard listener (`web.host:web.port`, default `127.0.0.1:9090`), Connect protocol over HTTP/1.1 or HTTP/2, same origin. |
| Web UI sessions | `ws(s)://<dashboard>/ws/attach`: one websocket per interactive session (browsers cannot do bidi Connect). |

Every dashboard request must carry an allowed `Host` header (see
`internal/web`). Websockets must be same-origin.

## Observing state: `Watch`

`Watch` streams `WatchResponse{revision, snapshot | change | heartbeat}`:

1. **A snapshot comes first** and replaces all client state: daemon info,
   projects, services, tasks and git status.
2. **Then changes follow.** Each change is an upsert of an entity
   (`project`, `service`, `task`, `git`, `daemon`) or `removed{kind, project,
   name}`. Revisions increase strictly.
3. **The server may send another snapshot at any time** (a resync after the
   client fell behind). Treat it as a full replacement.
4. **Heartbeats** arrive every 15s so dead connections are noticed.

Entity keys: projects by `id`, services and tasks by `project/name`, git
status by `project`. `GetState` returns the same snapshot once, for scripts
and the CLI.

Mutating RPCs return once the owning actor has accepted the command:

- `Stop*` returns once processes are gone.
- `Start*` and `RunTask` return once the start is under way.

The resulting transitions arrive through `Watch`.

### Status vocabulary

| Entity | Field | Values |
|---|---|---|
| Project | `status` | `stopped`, `starting`, `running`, `degraded`, `stopping`, `error` (`error` carries the message) |
| Project | `desired` | `running`, `stopped`, `partial` (a selected subset of services) |
| Service | `status` | `stopped`, `waiting` (dependencies), `building`, `starting`, `running`, `stopping`, `backoff`, `exited`, `failed` |
| Service | `health` | `""` (no `ready` probe or not running), `starting`, `healthy`, `unhealthy` (with `health_detail`) |
| Task | `status` | `idle`, `waiting`, `running`, `stopping`, `exited`, `failed` |

`message` explains the current state in words, for example "waiting for
db", "restarting in 4s", "killed by SIGKILL" or "build failed (exit code
2)".

Specs (`ServiceSpec`, `TaskSpec`) describe the resolved definition:
`command` is the display form of `run`, `ports` carry the decided numbers
(`auto` marks allocated ones), `ready` is the probe (`kind` and the `target`
URL, address or command), and `env_keys` names the variables the project
defines (values are never sent). `Project.env_files` lists the env files that
exist and `links` the project's links.

## Errors

Errors carry Connect codes, and clients switch on the code:

| Code | Meaning |
|---|---|
| `NotFound` | Unknown project, service, task, session or run. |
| `InvalidArgument` | Bad input, e.g. an invalid project id (ids are slugs; `..` is rejected). |
| `AlreadyExists` | A different config already registered this project id. |
| `FailedPrecondition` | Already or not running, config error, git failure, or a git operation already in progress. |
| `Unavailable` | The daemon is shutting down. |

## Logs

`Logs(project, sources[], run_offset, tail, before_seq, follow)` streams
`LogsResponse{lines[], has_more_before, new_run[]}`.

- **Sources.** An empty `sources` means every service of the project, merged
  by timestamp. A source is `{kind: service|task, name}`.
- **Run selection.** `run_offset` 0 is the current run, -1 the previous run,
  and so on. Only the current run can be followed.
- **History.** Up to `tail` lines of history are sent first (0 means
  everything, capped at 20k). `has_more_before` tells the client whether it
  can page back.
- **Paging.** `before_seq` pages backwards through one source.
- **Following.** With `follow`, new lines stream as they are written. When a
  source starts a new run, `new_run` names it.
- **Line format.** `LogLine{source, run, seq, ts_unix_nanos, stream:
  stdout|stderr|system, text}`. `text` keeps SGR colors only (other escape
  sequences are stripped, and `\r` progress redraws keep their final state).

## Interactive sessions: `Attach`

`Attach` is bidirectional. The first request must be `open{target, cols,
rows}`:

- `target.kind = terminal`: a new shell in the project directory. Pass
  `session_id` to reattach to an existing one.
- `target.kind = task | service`: the running process (tasks default to a
  TTY; services need `tty: true`).

The server replies `ready{session_id, tty}`, then streams `output` bytes
(starting with a replay of recent output) and finally `exit{exit_code,
message}`. The client sends `input` bytes, `resize`, `close_stdin` (EOF for
non-TTY processes) or `close` (end a terminal). Ending the stream only
detaches; the process keeps running.

The websocket equivalent (`/ws/attach`):

- **Client text frames:** `{"type":"open","target":{...},"cols":N,"rows":N}`,
  `{"type":"resize",...}`, `{"type":"eof"}`, `{"type":"close"}`.
- **Client binary frames:** input bytes.
- **Server text frames:** `{"type":"ready","session_id":...,"tty":bool,"stdin":bool}`,
  `{"type":"exit","exit_code":N}`, `{"type":"error","message":...}`.
- **Server binary frames:** output bytes.

## Tasks

`RunTask` returns `{run}` immediately. The run belongs to the daemon: it
survives the caller disconnecting and daemon restarts. A concurrent second
run fails with `FailedPrecondition`. `devyard run` attaches to the run and
forwards the terminal.

## Stats

`Stats(project, interval_ms)` streams per-process CPU% and RSS for every
running service and task of the project. The first message arrives after one
interval.

## Daemon restart

`RestartDaemon{restart_services}` responds, then the daemon drains, releases
its listeners and lock, and spawns its replacement. Services keep running
(they belong to their runners) unless `restart_services` is set. Clients
wait for `GetDaemon` to report a new pid.

## Changing the protocol

1. Edit `proto/devyard/v1/control.proto` and run `buf lint && buf generate`.
2. Implement the handler in `internal/api`.
3. Update the CLI (`internal/cli`) and the web UI (`web/src/data`).
4. Update this document.
