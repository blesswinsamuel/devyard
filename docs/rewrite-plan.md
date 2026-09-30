# Rewrite plan: API v2 + web UI

Status: proposed. Work happens on a long-lived `rewrite` branch; `master`
keeps the current UI until the new one reaches parity, then the branch merges
and the old code is deleted.

## Decisions

| Question | Decision |
|---|---|
| Rewrite the backend from scratch? | **No.** Redesign the **API** first and refactor the backend where the new API needs it. See [Backend](#backend-api-first-not-a-rewrite). |
| Stack | Keep SolidJS, Vite, Tailwind v4, Kobalte/zaidan, ConnectRPC, bun. Add `@solidjs/router`, Solid `createStore`+`reconcile` for live entities, TanStack Solid Query for request/response data, `@tanstack/solid-virtual`, Vitest, Playwright. |
| Layout | URL-driven pages, plus a bottom **dock** for terminals, interactive sessions and side-by-side logs. Drop the free-form pane tree. |
| Logs | Virtualized DOM log viewer. xterm is used only for interactive sessions (terminals, running TTY tasks, attached TTY services). |
| Tasks | Interactive: stdin input, PTY by default, detached from the client that started them. |
| Git | Full parity, nothing trimmed. |
| Protocol | Breaking `devyard.v1` → `devyard.v2`, no compatibility layer. |

## Backend: API first, not a rewrite

The backend is about 15k lines of non-generated Go, with good test coverage
(supervisor, orchestrator, control, and a 1.4k-line e2e suite). Many of its
behaviors took real work to get right:
- shim-based process adoption across daemon restarts
- `setpgid`/`killpg` teardown
- the health state machine
- restart backoff
- the TLS reverse proxy
- autostart and `.stopped` markers

Rewriting all of that buys the user nothing and risks regressions. The UX
problems come from the **API surface**. Fix that first, because the new UI's
data layer is built around it:

1. **`Watch` replaces `SubscribeEvents` + `List*` polling.** It sends a full
   snapshot on connect (projects, services, tasks, ports, git status), then
   entity-level upserts and removals, each with a monotonically increasing
   revision. Reconnecting means a new snapshot. This removes the refetch
   storms (today every service event triggers a refetch of
   projects+services+tasks+ports) and fixes gaps after a reconnect.
2. **Service and task spec exposed**, as a `spec` field on the entity:
   command, working_dir, depends_on with conditions, restart policy,
   healthcheck summary and last failure, ports/URLs, tty, shell, env **keys**
   (values withheld).
3. **Logs v2**:
   - `Logs{project, sources[], run: current|previous, tail, before_seq, follow}`
     streams `LogBatch{repeated LogLine{source, seq, ts, stream, text}}`.
   - Empty `sources` means a merged project stream.
   - `before_seq` lets the UI page back through history.
   - Timestamps are parsed server-side. Service logs are already timestamped
     by the shim; task logs are brought into line.
4. **`Stats` stream**: a server-pushed CPU/RSS sample per service every second
   while subscribed. It reuses `procstat`; `Top` stays for the CLI.
5. **Tasks become detached, interactive runs** (details in the next section).
6. **Sessions layer**: one server-side abstraction for bidirectional PTY I/O.
   It covers project terminals (today's `internal/web/pty.go`), running TTY
   tasks, and attaching to TTY services. The supervisor's unused
   `Attach`/`WriteInput`/`Resize` get wired up here. The browser reaches it
   over the existing `/ws` endpoint, since connect-web can't do bidi
   streaming. Frames: `open{kind, project, name, cols, rows}`, `input`,
   `resize`, `output`, `exit`, `close`.
7. **Lifecycle cleanup**: explicit `ReloadProject` (today it's overloaded on
   `StartProject`), `RemoveProject` exposed in the UI, `build` flag exposed
   ("Rebuild & start").
8. The CLI moves to v2 in the same phase, and the docs are updated
   (`control-protocol.md`, `architecture.md`, `config-schema.md`).

`supervisor.go` (2k lines) and `orchestrator.go` (1.5k) get split along the
seams these changes touch (tasks, logs, sessions). There's no rewrite for its
own sake.

## Interactive tasks

Current problems:
- `RunTask` runs the task as a direct daemon child with piped stdout/stderr
  and **no stdin**.
- It kills the task when the calling RPC's context ends
  (`supervisor.go` `RunTask`). A browser reload or a closed tab kills a
  running task.
- Tasks don't survive a daemon restart.
- Task log lines have no timestamps.

New design:
- **Task processes run through the shim**, which already supports PTYs,
  writes timestamped logs, and survives daemon restarts. `shim.Config` is
  generalized from service-only to service-or-task.
- **`tty` defaults to `true` for tasks** (it stays `false` for services).
  Prompting CLIs (inquirer, `npm init`, `read -p`) generally need a real TTY.
  `tty: false` opts out.
- **`RunTask` returns a `run_id` right away**, so the run isn't tied to the
  caller. `devyard run <task>` in the CLI attaches and still stops the task
  on Ctrl-C, by calling `StopTask` explicitly, so CLI behavior is unchanged.
  The CLI also forwards stdin when it's attached to a terminal.
- **UI**:
  - While a TTY task is running, its page shows a live xterm session with
    full keyboard input and resize, and it can be popped into the dock.
  - A non-TTY task shows the log viewer plus a single-line stdin input.
  - After exit, the run is shown in the log viewer, with a current/previous
    run selector.
  - "Run with args…" is available from the page, palette and context menu.
- TTY **services** get the same "Attach" affordance on their page.

## Web UI

### Information architecture

- **Sidebar**: project tree (`role="tree"`) with a filter box, live status,
  health, git badge and failing-count badge. Every row's context menu is
  generated from the action registry.
- **`/`**: all-projects table (status, running/total services, health,
  branch, URLs, start/stop), global ports & URLs, and onboarding when empty.
- **`/projects/:p`**: project dashboard.
  - Header: status, config path, branch, Start / Stop / Reload /
    Rebuild & start / Remove.
  - Service table: status, health, uptime, restarts, pid, CPU/mem sparklines,
    URLs, actions.
  - Tasks list with last exit code and duration.
  - Small depends_on graph.
  - Merged log panel with per-service filter chips.
- **`/projects/:p/services/:s`**: Logs | Details | Metrics tabs, plus
  Attach for TTY services.
- **`/projects/:p/tasks/:t`**: live session or logs, run with args, run
  selector.
- **`/projects/:p/git`**: see [Git](#git).
- **`/settings`**: global config, daemon status and restart, theme,
  shortcuts reference. This replaces three modals.
- **Dock** (bottom, collapsible, persists across navigation): terminal tabs,
  interactive sessions, pinned log streams (the "compare two services" use
  case that splits serve today).

### Log viewer

- Virtualized rows; SGR parsed to spans (`lib/ansi.ts` already strips
  everything else).
- Follow-tail with a "jump to latest" pill; loads earlier lines on
  scroll-up via `before_seq`.
- Search with next/prev, text/regex filter, wrap toggle, timestamps toggle,
  service prefix column in merged view.
- Linkified URLs, copy line or selection, restart marker lines.
- Must hold up under a 10k lines/s stream; this gets profiled in the
  polish phase.

### Cross-cutting

- **Action registry** (`data/actions.ts`): `{id, label, icon, shortcut,
  when(ctx), confirm?, run(ctx)}`. The command palette (⌘K), context menus,
  dropdowns, header buttons and single-key shortcuts all read from it, so
  they can't drift apart.
- **Confirmations**: AlertDialog for kill / stop project / remove project;
  undo toasts for recoverable actions.
- **Pending state per entity and action**: spinners, no double-fire.
- **Crash awareness**: a toast with "View logs" when a service exits
  non-zero or turns unhealthy; optional browser notification; failing count
  in the tab title and favicon.
- **One connection state machine** for `Watch` and `/ws`, one banner, stale
  data dimmed.
- **Design tokens** for density, type scale and semantic status colors; no
  arbitrary pixel sizes in app code.
- Reduced-motion support, visible focus rings, a per-page mobile layout.
- One versioned localStorage namespace (`devyard:*`); `lc-*` keys dropped.

### Code layout

```
web/src/
  app/          routes, shell layout, providers
  data/         connection.ts, entities.ts (createStore), queries.ts, actions.ts, sessions.ts
  features/     home/ project/ service/ task/ logs/ git/ terminal/ settings/ palette/
  components/   app wrappers over ui/
  components/ui/  zaidan-generated, untouched
  lib/          ansi, format, keyboard, persistence
```

### Git

Full parity with today's `GitView`:
- commit graph and log search
- branches, remotes, tags and stashes
- commit diff with adjustable context and per-file collapse
- file list with search
- stage/unstage (per file and all), commit
- push, pull and fetch with shared progress
- WORKDIR view and deep links to commits

**Nothing is trimmed.** The only changes are structural:
- split the 1.5k-line component into commit list/graph, diff, files, refs
  and commit box
- virtualize the commit list
- add keyboard navigation (j/k through commits and files)
- fetch data through Query, invalidated by `GitChanged` from `Watch`

## Quality gates (added to CI)

- `tsc --noEmit` (`vite build` doesn't typecheck today).
- Vitest for the data layer, action registry, ANSI parser and log buffer.
- Playwright e2e against the real binary with a fixture project and isolated
  XDG dirs, the same way `test/integration` works.
- The Go side follows the existing rules: unit tests per package, and e2e
  cases for detached tasks, stdin/PTY sessions, `Watch` snapshots and merged
  logs.

## Phases

Each phase is a set of focused, build-clean commits on `rewrite`.

1. **API v2 + backend.** Proto v2, `Watch`, logs v2, `Stats`, specs,
   detached shim-backed interactive tasks, sessions layer, lifecycle
   cleanup, CLI on v2, docs. The old UI isn't maintained on the branch past
   this point.
2. **UI foundations.** Dependency cleanup, router, connection and entity
   store, Query, action registry, design tokens, app shell, test harness,
   CI gates.
3. **Sidebar, home, project dashboard, command palette.**
4. **Log viewer, service and task pages** (Details, Metrics, Attach,
   interactive task sessions).
5. **Dock**: terminals, sessions, pinned logs.
6. **Git**, at full parity.
7. **Settings, notifications, mobile/a11y/perf passes**, then delete the
   old code and update `AGENTS.md`, `README.md` and `docs/`. Merge.
