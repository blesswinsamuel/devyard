# Adding a CLI command

A checklist for adding `devyard <command>` end to end. The example is a
hypothetical `devyard pause <service>` that sends SIGSTOP and marks the
service paused.

## Decide what kind of command it is

- **Daemon command**: talks to the daemon over its socket (`status`, `logs`,
  `restart`, `stop`). Needs an RPC, unless an existing one already covers it.
- **Local command**: works on the config directly without a daemon (`build`).
- **Daemon management**: manages the daemon process itself (`daemon start`,
  `daemon stop`, `daemon restart`).

## Steps for a daemon command

### 1. Protocol

Add the RPC to `proto/devyard/v1/control.proto`:

```proto
rpc PauseService(PauseServiceRequest) returns (PauseServiceResponse);

message PauseServiceRequest {
  string project = 1;
  string service = 2;
}
message PauseServiceResponse {}
```

Then run `buf lint && buf generate` and update
[control-protocol.md](control-protocol.md).

### 2. Engine

Add the behavior to the owning actor in `internal/engine`. For a service,
that means `internal/engine/service.go`:

- Add a command type (`svcPause{reply chan error}`).
- Add the public method `(*Service).Pause(ctx)`, which sends the command and
  waits for the reply.
- Handle the command in `serviceActor.loop`, and publish any state change
  with `a.publish()`.
- Return typed errors (`ErrNotRunning`, ...).

Never touch actor state from another goroutine. Long waits run in a goroutine
that posts an event back to the mailbox.

Add a test in `internal/engine/engine_test.go`. It uses real runners and the
recorder observer.

### 3. API

Implement the handler in `internal/api` (for example `server.go`). Resolve the
entity with `s.service(project, name)`, call the engine, and return errors
through `toConnect`.

### 4. CLI

Add the cobra command in `internal/cli`:

- Resolve the daemon with `c.dial(ctx)`, or `c.ensureDaemon(ctx)` if the
  command may start one.
- Resolve the project with `c.projectID(ctx, cl)`. It honours `-p` and the
  local config, and an unknown `-p` is an error.
- Print data to stdout. Print status (`devyard: paused "api"`) to stderr.
- Support `-o json` through `c.printProtoJSON` when the command lists
  entities.

Register the command in `NewRootCommand`, and in the `service` group if it is
per-service.

### 5. Web UI

Add an entry to the action registry (`web/src/data/actions.ts`). The palette,
context menus, buttons and shortcuts all pick it up from there.

### 6. Tests and docs

- Add an e2e case in `test/e2e/cli` (and `test/e2e/api` for API semantics),
  using the harness.
- Update the README command tables.

## Gotchas

- **Commands reply only when their effect holds.** For example, pause should
  reply once the signal was delivered.
- **Don't add error-string matching** anywhere. Add a sentinel error and map
  it in `api.toConnect` if clients need to tell it apart.
