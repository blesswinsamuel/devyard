# Rewrite plan

Status: proposed. The work happens on the `rewrite` branch. Existing code is
replaced in place, with no versioned names (no `v2`) and no compatibility
layers. `master` keeps working until the branch merges.

## Why the backend comes first

A code audit (2026-09-30) found about 35 concrete backend bugs. They're listed
in the [bug ledger](#bug-ledger). Almost all of them come from six structural
causes:

1. **No single owner for a service's or project's state.** Status, pid/pgid,
   `done` channels and stop flags are changed from 7+ goroutines, each using a
   different mix of mutexes, atomics and nothing at all. Status is assigned at
   about 15 separate places, and the orchestrator does lock → check → unlock →
   slow work → re-lock.
2. **Child processes are tied to the daemon's lifetime.** Their stdout and
   stderr are pipes into the daemon, and TTY output goes to a PTY master the
   daemon holds. `internal/shim` was built to solve this, but nothing launches
   it. So "adoption" across daemon restarts breaks services through
   SIGPIPE/SIGHUP.
3. **State lives in five places**: the in-memory map, `.stopped`,
   `config-path`, `config.snapshot.json` and `state.json`. There are also
   hidden inputs: the daemon's own environment, and a forgotten `--env-file`.
   A read-only RPC (`ListProjects`) can even delete a project's data.
4. **Events are fire-and-forget.** There's no snapshot and no revision number,
   events are silently dropped when a buffer fills, and some state changes emit
   nothing. Clients have to poll and refetch.
5. **Errors are plain strings.** Nearly everything is `CodeInternal`, and the
   UI suppresses errors by regex-matching `/not running/`.
6. **Destructive operations trust any input.** Project names aren't validated
   (`..` resolves to `~/.local/state`), the web server has no Host check, and a
   DNS-rebinding page could open a shell.

Fixing these one at a time is what has produced 92 fix commits out of 295. The
plan replaces the **core** (supervisor, orchestrator, daemon lifecycle,
control/web servers, CLI wiring) with an architecture that rules these classes
out. It **keeps the leaf packages** that are sound, fixing their listed bugs:
`config`, `dag`, `health` probes, `proxy`, `procstat`, `gitlog`, `gitwatcher`,
`globalconfig`, `ui`.

## Backend architecture

### Runner: every process goes through the shim

`devyard --shim` is the **only** way a service or task process gets launched.
Each shim:

- Starts the child in its own session and process group. For a PTY, it uses
  `Setsid` only. The `Setpgid`+`Setsid` combination is what makes every
  `tty: true` service fail today with EPERM.
- **Owns the child's stdio.** It writes logs as structured records (seq,
  timestamp, stream, text) into per-run files. Rotation happens within a run,
  so there's never a previous-run slot to overwrite. It holds the PTY master
  and stdin.
- Records the exit status to a file, and uses `WaitDelay`/bounded drain. That
  way a grandchild that escapes the process group but keeps stdout open can't
  wedge the service.
- Serves a small per-process Unix control socket with operations for status,
  signal, attach (output replay + live), input, and resize.

The daemon only talks to shims. That makes a fresh launch and an adopted
process **the same code path**: adopting means reconnecting to the shim's
socket. Daemon restarts and crashes no longer affect children at all.

### Engine: one actor per service, task and project

- Each service, task and project is **one goroutine** that owns its state,
  reads a command/event mailbox, and applies one pure `reduce(state, event)`
  function. Commands are Start, Stop, Restart, Kill, ChildExited,
  HealthChanged, BackoffElapsed and DepsReady.
- **Commands complete only when done.** `Stop` replies once the process group
  is gone, escalating from SIGTERM to SIGKILL. `Restart` means stop, then
  start, inside the same actor. It's impossible for two run loops to exist.
  Backoff and dependency waits are timers and events inside the actor, so a
  stop interrupts them.
- **Every state has an exit.** Every non-terminal state has a transition out,
  and this is checked by table tests plus **model-based randomized tests**:
  random command sequences asserting four invariants:
  - never more than one live process
  - `Stop` always completes
  - `Kill` always works, even while stopping
  - status eventually settles into a terminal or live state
- **Health checkers are created per run,** with a run ID and a `start_period`.
  Dependents wait for the "healthy for run N" event instead of polling
  `startedOnce` flags that never get reset.
- **The project actor serializes lifecycle commands**: start, stop, reload,
  remove, and start-selected. It tracks a generation counter. A daemon-level
  **draining** state rejects new commands during shutdown. Project status
  comes from actor state, never from a `WaitGroup`.
- **Reload diffs the config**: services whose spec changed restart, removed
  ones stop, new ones start. Proxy routes switch only when the new process is
  up.
- **Registries are immutable snapshots**, swapped atomically. Readers never
  touch a map that's being written.

### State: one file per project, written only by its actor

`$XDG_STATE_HOME/devyard/projects/<id>/project.json`, written atomically. It
holds:

- the config path and env file
- the **captured launch environment** (see decisions)
- the desired state: running, stopped, or a selected set of services
- a hash of the resolved config
- the shim sockets and run IDs

Rules:

- **Reads never modify it.** A missing config shows up as a `config_missing`
  status and is never deleted.
- **Project identity is a validated slug.** Two checkouts with the same
  directory name conflict loudly instead of merging.
- All paths stay confined under the app dir.

### Daemon lifecycle

- **The pidfile is written by the lock holder**, not by whoever spawned the
  daemon.
- **Restarting the daemon** is: drain, release the listeners and lock, then
  exec the replacement. Because the shims keep services alive, "restart but
  keep services" is trivial. "Restart services" means stopping them through
  the actors first.
- `ensureDaemon` waits for **ready**, meaning autostart is registered, not
  just for the socket to answer.

### API (the existing `control.proto` is replaced in place)

- **`Watch`** returns a snapshot at revision N, then entity-level deltas.
  When a subscriber falls behind, it gets a `resync` instead of silently
  losing events. Every actor transition publishes an event. This replaces
  `SubscribeEvents` and the `List*` polling.
- **Entities carry their spec**: command, working dir, `depends_on` with
  conditions, restart policy, healthcheck and its last failure, ports and
  URLs, tty, and env **keys** only.
- **`Logs`** takes `{project, sources[], run, tail, before_seq, follow}` and
  streams batches of `{source, seq, ts, stream, text}`. An empty `sources`
  list means a merged project stream. It never loses lines at rotation.
- **`Stats`** streams CPU and memory per service while subscribed. It fixes
  the Apple Silicon CPU unit bug.
- **Tasks** have their own RPCs. `RunTask` returns a `run_id` right away and
  the run isn't tied to the caller. `StopTask` escalates. Tasks are stopped
  along with their project.
- **Sessions**: one bidirectional PTY abstraction covering project terminals,
  running tasks, and attached TTY services. It's used over `/ws` by the
  browser, and over the control socket by the CLI.
  - Sessions **outlive connections**: reconnecting reattaches with scrollback
    replay.
  - When output backs up, it's coalesced or dropped for that one session,
    instead of killing every terminal.
- **Explicit lifecycle RPCs**: `ReloadProject`, `RemoveProject` (only for
  registered projects, and it fails if the name is unknown), and
  `build: true` on start.
- **Typed errors** (`ErrProjectNotFound`, `ErrNotRunning`, …) map to proper
  Connect codes. Clients switch on codes, never on message text.
- **Web security**: a Host allowlist (loopback plus the configured domain
  suffix) and a per-install token (cookie) are required for RPC and `/ws`.
  This closes the DNS-rebinding shell hole.

### CLI

A thin client of the same API.

- **Name resolution goes through the daemon.** `-p unknown` is an error,
  never a silent fallback to the cwd's config.
- `devyard run <task>` attaches to the task session with stdin forwarding;
  Ctrl-C calls `StopTask`.
- Commands have context deadlines, so a wedged daemon can't hang the CLI
  forever.

## Tasks: interactive by default

- Tasks are shim-backed, so they survive browser reloads and daemon restarts.
- **`tty` defaults to `true` for tasks** (it stays `false` for services), so
  prompting CLIs work.
- In the UI, a running TTY task is a live xterm session that takes keyboard
  input and can be popped into the dock. A task with `tty: false` gets the log
  viewer plus a one-line stdin input. Finished runs are shown in the log
  viewer, with a run selector. "Run with args…" is available everywhere.
- TTY services get the same **Attach** option.

## E2E tests: rewritten, hermetic, with Docker optional

The audit confirmed the current suite **leaks into the host**. On this machine,
`TestE2E_Proxy` fails because the test daemon falls back to web port 9090,
which the real daemon is using. The gitlog unit tests fail because they
inherit the user's `~/.gitconfig` (`push.autoSetupRemote`).

The fix is a proper sandbox rather than Docker as the default. The reason:
macOS GitHub runners have no Docker, and Docker on a Mac runs Linux, which
would stop testing the macOS-only code paths (libproc, socket path limits,
process groups).

- **`test/e2e/harness`**:
  - **Allowlisted environment.** Only PATH, TERM and LANG are passed through.
    HOME, TMPDIR, every `XDG_*`, CAROOT, SHELL and the `GIT_CONFIG_*`
    variables are all sandboxed. A guard aborts the run if the resolved socket
    path equals the real one.
  - **`port: 0`** for the web server and proxy, with the bound addresses
    reported by the daemon. This removes free-port races.
  - **Typed assertions** through the Connect client and `Watch` events, never
    by parsing `ps` output.
  - **`Eventually`** with deadline scaling (longer under `-race`). On failure
    it dumps the last state, the daemon log and the service logs.
  - **A leak checker.** It tracks every pgid the test sees and fails if any
    survive. Every process gets tagged with `DEVYARD_SANDBOX_ID`, and a
    sweeper kills only tagged leftovers, including after a crash or Ctrl-C.
  - **Go fixture programs** (`httpecho`, `ticker`, `exiter`, `prompter`)
    replace `python3` and fixed ports.
- **Suites**: `test/e2e/{cli,api,events,daemon,tasks,sessions,proxy,git,chaos}`.
  - `chaos` covers SIGKILLing the daemon mid-operation, restart handover
    while services are writing output, and randomized command sequences
    against a real daemon.
- **Every bug in the ledger gets a regression test.** These are written
  first, against the current code, so they're expected to fail until the
  rewrite fixes them.
- **Browser e2e**: Playwright specs in `web/e2e`. `globalSetup` starts
  `go run ./test/e2e/cmd/sandbox`, a hermetic daemon plus a fixture project.
- **Docker**: `scripts/e2e-linux.sh` runs the same suite inside one `golang`
  container, for Linux coverage from a Mac. It's a single `docker run`;
  testcontainers isn't needed, because the sandbox, not the container, provides
  isolation.
- **CI**: native runs on Ubuntu and macOS, with `-race`.

## Web UI

This part is unchanged from the previous revision of this plan.

- **Stack**: SolidJS, Vite, Tailwind v4, Kobalte/zaidan, ConnectRPC, bun.
  Added: `@solidjs/router`, a `createStore`+`reconcile` entity store fed by
  `Watch`, TanStack Solid Query for request/response data, and a virtualized
  DOM log viewer. xterm is used only for interactive sessions.
- **Layout**: pages driven by the URL, plus a bottom dock for terminals,
  sessions and pinned logs. The sidebar is a tree.
- **Pages**:
  - Home: all projects.
  - Project dashboard: service table with sparklines, tasks, dependency graph,
    and merged logs.
  - Service page: Logs / Details / Metrics / Attach.
  - Task page.
  - Git page.
  - Settings page.
- **Log viewer**: follow mode, search, filter, wrap, timestamps, paging back
  through history, merged sources, and links.
- **One action registry** drives the ⌘K palette, context menus, buttons and
  shortcuts. Destructive actions use AlertDialog confirmations, and actions
  show pending states.
- **Crash toasts and notifications**, plus a single connection state machine.
- **Design tokens**, reduced motion support, focus rings, and layouts designed
  per page for mobile.
- **Git**: full parity, nothing trimmed. The component gets split up, the
  commit list virtualized, and j/k navigation added.
- **Quality gates**: `tsc --noEmit`, Vitest, and Playwright, all in CI.

## Decisions made (object if you disagree)

- **Launch environment is captured at `start` and persisted** (0600) per
  project. Services then behave like the shell that started them, with the
  same PATH and version managers. Today they inherit whichever shell first
  spawned the daemon. The trade-off: env values sit on disk in the user's
  state dir.
- Project identity is a validated slug; the default is still the directory
  name.
- Tasks default to `tty: true`.
- The daemon web UI requires a token cookie. Opening the UI from the CLI
  (`devyard web`, or the URL printed by `start`) sets it.

## Phases

Each phase is a set of focused, build-clean commits on `rewrite`.

1. **Harness and regression tests.** Hermetic e2e harness, fixture programs,
   port the existing coverage, and write a failing regression test for each
   ledger entry. Fix the two host-leak failures.
2. **Runner.** The shim as the only launcher: structured logs, exit files,
   control socket, PTY fixes.
3. **Engine.** Service, task and project actors with reducers, model-based
   tests, per-run health, and config diffing on reload.
4. **State and daemon.** `project.json` store, registry, daemon lifecycle,
   the revisioned event bus, and typed errors.
5. **API and servers.** New `control.proto`, control and web servers,
   sessions, web security, and the CLI moved onto them. By this point the
   ledger tests pass. Delete the old core.
6. **UI foundations.** Router, entity store, Query, action registry, tokens,
   app shell, test gates.
7. **Sidebar, home, project dashboard, command palette.**
8. **Log viewer, service and task pages**, including interactive tasks and
   Attach.
9. **Dock**: terminals, sessions, pinned logs.
10. **Git** at full parity.
11. **Settings, notifications, passes for mobile, accessibility and
    performance.** Delete the old UI, update the docs, merge.

## Bug ledger

S = supervisor/shim/health, O = orchestrator/daemon/control/web. Each entry
gets a regression test in phase 1 and is fixed by the phase named.

| ID | Bug | Fixed by |
|---|---|---|
| S1 | `tty: true` services never start (`Setpgid`+`Setsid` → EPERM); same bug in the shim | Runner (2) |
| S2 | Concurrent `Restart`s create duplicate run loops; orphaned processes; double close of `done` crashes the daemon | Service actor (3) |
| S3 | Stop/restart can't interrupt backoff or dependency waits; `restarts` never reset; `on-failure` budget counts over the whole daemon lifetime | Service actor (3) |
| S4 / O12 | `services`/`order`/`File`/`taskRuntimes` maps read without locks during `Reconcile` → fatal concurrent map access | Immutable registries (3) |
| S5 / O4 | Adoption breaks services: daemon-owned pipes/PTY → SIGPIPE/SIGHUP; adopted stop never escalates; stale pgid; exit code −1 triggers `on-failure` | Runner (2) |
| S6 | Two concurrent `RunTask` calls both start; clobbered state; double close | Task actor (3) |
| S7 | `KillService` does nothing while a stop is in progress; non-fatal signals leave status stuck at `stopping` | Service actor (3) |
| S8 | Services stuck in `stopping`/`backoff`; `start <svc>` can't recover them | Reducer exhaustiveness (3) |
| S9 | Health checker outlives restarts; `service_healthy` passes for a dead dependency; no `start_period` | Per-run health (3) |
| S10 / O6 | A grandchild holding stdout wedges the run loop; stop hangs forever and blocks every later project command | Runner drain (2), actor deadlines (3) |
| S11 | Task: 64 KiB line limit hangs it; no SIGKILL escalation; not stopped with its project; adopted tasks never exit | Task actor + runner (2–3) |
| S12 | `top` CPU about 42× too low on Apple Silicon (Mach ticks treated as nanoseconds) | procstat fix (4) |
| S13 | Daemon restart overlaps old and new daemons; the old one's `RemoveState` deletes the new one's state | Daemon lifecycle (4) |
| S14 / O14b | Follow loses lines at rotation; `--previous` can show part of the current run | Per-run log files (2) |
| S15 | Out-of-order event delivery can flip an exited service back to `running` | Actor publishes in order (3–4) |
| S16 | A stop between the stop check and pgid recording skips SIGTERM, then SIGKILLs after 20s | Service actor (3) |
| S17 | Unsynchronized `done`/`status`; `wg.Add` races `wg.Wait` | Service actor (3) |
| O1 | `ListProjects` deletes a live project's state and logs when its config is briefly missing; processes keep running with no way to reach them | State store (4) |
| O2 | Project shows "stopped" while services run; later stop/start orphans them and duplicates them | Project actor (3) |
| O3 | Concurrent starts (e.g. `start` during autostart) launch duplicates and delete shared state | Project actor + ready-wait (3–4) |
| O5 | `rm` acts on the wrong project via cwd fallback; `project rm ..` can wipe `~/.local/state`; `rm` reports success on failure | Slugs + CLI resolution (4–5) |
| O7 | DNS rebinding → web terminal → shell | Web security (5) |
| O8 | Restart-with-services times out the replacement daemon; commands accepted during shutdown are orphaned | Draining + lifecycle (4) |
| O9 | A stop during start is lost; a resume racing a stop drops the `.stopped` marker | Project actor (3) |
| O10 | Reload forgets `--env-file`; spec changes don't restart services; proxy switches before the new process is up; services inherit the daemon's environment | Config diff + captured env (3–4) |
| O11 | Projects with the same name silently merge | Slug identity (4) |
| O13 | Events dropped on overflow; subscriber misses events before its first heartbeat; some transitions never emit | `Watch` bus (4) |
| O14 | One output burst kills every terminal on the connection; a reload kills all shells | Sessions (5) |
| O15 | Errors are all `CodeInternal`; the UI matches message text | Typed errors (4–5) |
| O16 | Duplicate daemons mislabel the pidfile; the pidfile is removed unconditionally | Daemon lifecycle (4) |
| O17 | `project add --start=false` starts and then stops every service | CLI (5) |
| O18 | gitwatcher: one owner per path (shared repos steal events); stopped projects not watched; new ref directories missed | gitwatcher fix (4) |
| O19 | git push/pull/fetch and builds have no timeout, ignore context, and run without their own process group | gitlog/build fix (4) |
| O20 | Projects that fail to load during autostart vanish from the list | Registry (4) |
| O21 | Web-started tasks are killed when the browser tab reloads or closes | Task actor (3) |
| T1 | e2e `TestE2E_Proxy` binds the real daemon's port 9090; fixtures use fixed ports 3000–3002 and python3 | Harness (1) |
| T2 | gitlog tests inherit the user's `~/.gitconfig` | Harness env (1) |
| T3 | `TestStopDaemonKeepServicesAndAdopt` is timing-dependent (reads the pid before adoption finishes) | Harness `Eventually` (1) |
