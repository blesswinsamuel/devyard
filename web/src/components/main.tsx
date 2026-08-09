import { For, createEffect, createMemo, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Play, Power, RefreshCw, RotateCcw, Skull, SquareTerminal, History } from "lucide-solid";
import {
  projects,
  services,
  actions,
  actionStates,
  selectedProject,
  selectedService,
  selectedAction,
  startProject,
  stopProject,
  restartService,
  startService,
  stopService,
  killService,
  runAction,
  theme,
  panelOpen,
  togglePanel,
  isPreviousLogs,
  togglePreviousLogs,
  activeView,
} from "~/store";
import { subscribeLogs, subscribeActionLogs } from "~/ws";
import { statusLabel, statusTone, healthTone, serviceMeta } from "~/lib/status";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "~/components/ui/tooltip";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { formatLogLine } from "~/lib/ansi";
import { BottomPanel } from "~/components/bottom_panel";
import { GitView } from "~/components/git_view";

type LogSubscribe = (
  project: string,
  target: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev?: boolean
) => () => void;

function LogTerminal(props: {
  project: string;
  target: string;
  active: boolean;
  prev: boolean;
  subscribe: LogSubscribe;
}) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;

  onMount(() => {
    const t = createTerminal(container);
    term = t;

    let buffer: string[] = [];
    let rafId: number | null = null;

    const flush = () => {
      if (buffer.length > 0) {
        t.write(buffer.join("\r\n") + "\r\n", () => {
          t.scrollToBottom();
        });
        buffer = [];
      }
      rafId = null;
    };

    const scheduleFlush = () => {
      if (rafId === null) {
        rafId = requestAnimationFrame(flush);
      }
    };

    const resetView = () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      buffer = [];
      t.reset();
      t.clear();
      t.write("\x1b[2J\x1b[3J\x1b[H");
    };

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    // Subscribe reactively: reruns when props.prev (or the target/stream
    // selection) changes, so toggling between live and previous-run logs tears
    // down the old stream and swaps the pane to the new content.
    createEffect(() => {
      resetView();
      const unsubLogs = props.subscribe(
        props.project,
        props.target,
        (line) => {
          buffer.push("\x1b[0m" + formatLogLine(line));
          scheduleFlush();
        },
        resetView,
        props.prev
      );
      onCleanup(() => {
        unsubLogs();
        resetView();
      });
    });

    onCleanup(() => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
      }
      t.dispose();
      term = null;
    });
  });

  createEffect(() => {
    if (props.active && term) {
      requestAnimationFrame(() => {
        term?.fit();
      });
    }
  });

  return (
    <div
      ref={container}
      class="h-full w-full overflow-hidden bg-background"
      classList={{ hidden: !props.active }}
    />
  );
}

function ServiceTerminal(props: {
  project: string;
  service: string;
  active: boolean;
  prev: boolean;
}) {
  return (
    <LogTerminal
      project={props.project}
      target={props.service}
      active={props.active}
      prev={props.prev}
      subscribe={subscribeLogs}
    />
  );
}

function ActionTerminal(props: {
  project: string;
  action: string;
  active: boolean;
  prev: boolean;
}) {
  return (
    <LogTerminal
      project={props.project}
      target={props.action}
      active={props.active}
      prev={props.prev}
      subscribe={subscribeActionLogs}
    />
  );
}

function useActiveLogTabs(
  getSelectedTarget: () => string | null,
  getAvailableItems: (project: string) => { name: string }[] | undefined
) {
  const [tabs, setTabs] = createSignal<{ project: string; name: string; key: string }[]>([]);

  const activeKey = () => {
    const p = selectedProject();
    const t = getSelectedTarget();
    return p && t ? `${p}/${t}` : null;
  };

  createEffect(() => {
    const p = selectedProject();
    const target = getSelectedTarget();
    const available = p ? getAvailableItems(p) : undefined;
    setTabs((prev) => {
      let filtered = p ? prev.filter((item) => item.project === p) : [];
      if (available) {
        const validSet = new Set(available.map((item) => item.name));
        filtered = filtered.filter((item) => validSet.has(item.name));
      }
      if (p && target && available && available.some((item) => item.name === target)) {
        const key = `${p}/${target}`;
        if (!filtered.some((item) => item.key === key)) {
          return [...filtered, { project: p, name: target, key }];
        }
      }
      return filtered;
    });
  });

  return { tabs, activeKey };
}

function LogViewer() {
  const { tabs, activeKey } = useActiveLogTabs(
    selectedService,
    (p) => services()[p]
  );

  return (
    <div class="relative h-full w-full">
      <For each={tabs()}>
        {(item) => (
          <ServiceTerminal
            project={item.project}
            service={item.name}
            active={activeKey() === item.key}
            prev={isPreviousLogs({ kind: "service", project: item.project, service: item.name })}
          />
        )}
      </For>
    </div>
  );
}

function ActionLogViewer() {
  const { tabs, activeKey } = useActiveLogTabs(
    selectedAction,
    (p) => actions()[p]
  );

  return (
    <div class="relative h-full w-full">
      <For each={tabs()}>
        {(item) => (
          <ActionTerminal
            project={item.project}
            action={item.name}
            active={activeKey() === item.key}
            prev={isPreviousLogs({ kind: "action", project: item.project, action: item.name })}
          />
        )}
      </For>
    </div>
  );
}

function ServiceHeader() {
  const project = () => selectedProject()!;
  const service = () => selectedService()!;
  const state = createMemo(() =>
    (services()[project()] ?? []).find((s) => s.name === service())
  );

  return (
    <>
      <div class="flex min-w-0 items-center gap-2">
        <span class="truncate text-xs text-muted-foreground">{project()}</span>
        <span class="text-muted-foreground">/</span>
        <span class="truncate font-semibold">{service()}</span>
        <Show when={state()}>
          {(s) => (
            <>
              <Badge variant={statusTone(s().status)}>
                {statusLabel(s().status, s().exit_code)}
              </Badge>
              <Show when={s().has_health}>
                <Badge variant={healthTone(s().has_health, s().health)}>
                  health: {s().health}
                </Badge>
              </Show>
              <span class="hidden truncate text-xs text-muted-foreground md:inline">
                {serviceMeta(s())}
              </span>
            </>
          )}
        </Show>
      </div>
      <div class="ml-auto flex items-center gap-1.5">
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant={isPreviousLogs({ kind: "service", project: project(), service: service() }) ? "secondary" : "outline"}
            size="sm"
            onClick={() => togglePreviousLogs({ kind: "service", project: project(), service: service() })}
          >
            <History class="size-4" />
            <span class="hidden md:inline">
              {isPreviousLogs({ kind: "service", project: project(), service: service() }) ? "Live" : "Previous"}
            </span>
          </TooltipTrigger>
          <TooltipContent>Show previous run's logs (p)</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant={panelOpen() ? "secondary" : "outline"}
            size="sm"
            onClick={togglePanel}
          >
            <SquareTerminal class="size-4" />
            Terminal
          </TooltipTrigger>
          <TooltipContent>Toggle Terminal Panel</TooltipContent>
        </Tooltip>

        <Show
          when={state()?.status === "stopped" || state()?.status === "exited"}
          fallback={
            <>
              <Tooltip>
                <TooltipTrigger
                  as={Button}
                  variant="secondary"
                  size="sm"
                  onClick={() => restartService(project(), service())}
                >
                  <RotateCcw />
                  Restart
                </TooltipTrigger>
                <TooltipContent>Restart service (r)</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger
                  as={Button}
                  variant="outline"
                  size="sm"
                  onClick={() => stopService(project(), service())}
                >
                  <Power />
                  Stop
                </TooltipTrigger>
                <TooltipContent>Stop service (s)</TooltipContent>
              </Tooltip>
            </>
          }
        >
          <Tooltip>
            <TooltipTrigger
              as={Button}
              variant="default"
              size="sm"
              onClick={() => startService(project(), service())}
            >
              <Play />
              Start
            </TooltipTrigger>
            <TooltipContent>Start service (r)</TooltipContent>
          </Tooltip>
        </Show>
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant="outline"
            size="icon"
            class="text-destructive hover:text-destructive"
            onClick={() => killService(project(), service())}
          >
            <Skull />
            <span class="sr-only">Kill service</span>
          </TooltipTrigger>
          <TooltipContent>Kill service (k)</TooltipContent>
        </Tooltip>
      </div>
    </>
  );
}

function ProjectHeader() {
  const project = () => selectedProject()!;
  const info = createMemo(() => projects().find((p) => p.name === project()));
  const isStopped = () => info()?.status === "stopped";

  return (
    <>
      <div class="flex min-w-0 items-center gap-2">
        <span class="truncate font-semibold">{project()}</span>
        <Show when={info()}>
          {(p) => (
            <Badge variant={statusTone(p().status)} class="capitalize">
              {p().status}
            </Badge>
          )}
        </Show>
      </div>
      <div class="ml-auto flex items-center gap-1.5">
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant={panelOpen() ? "secondary" : "outline"}
            size="sm"
            onClick={togglePanel}
          >
            <SquareTerminal class="size-4" />
            Terminal
          </TooltipTrigger>
          <TooltipContent>Toggle Terminal Panel</TooltipContent>
        </Tooltip>

        <Show
          when={!isStopped()}
          fallback={
            <Tooltip>
              <TooltipTrigger
                as={Button}
                variant="default"
                size="sm"
                onClick={() => startProject(project())}
              >
                <Play />
                Start project
              </TooltipTrigger>
              <TooltipContent>Start project (u)</TooltipContent>
            </Tooltip>
          }
        >
          <div class="flex items-center gap-2">
            <Tooltip>
              <TooltipTrigger
                as={Button}
                variant="outline"
                size="sm"
                onClick={() => startProject(project())}
              >
                <RefreshCw class="size-4" />
                Reload config
              </TooltipTrigger>
              <TooltipContent>Re-read local-compose.yml & prune orphans (up)</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                as={Button}
                variant="outline"
                size="sm"
                onClick={() => stopProject(project())}
              >
                <Power class="size-4" />
                Stop project
              </TooltipTrigger>
              <TooltipContent>Stop project (d)</TooltipContent>
            </Tooltip>
          </div>
        </Show>
      </div>
    </>
  );
}

function ActionHeader() {
  const project = () => selectedProject()!;
  const actionName = () => selectedAction()!;
  const act = createMemo(() =>
    (actions()[project()] ?? []).find((a) => a.name === actionName())
  );
  const state = createMemo(() =>
    (actionStates()[project()] ?? []).find((a) => a.name === actionName())
  );

  const status = () => state()?.status ?? "idle";
  const pid = () => state()?.pid ?? 0;
  const exitCode = () => state()?.exit_code ?? 0;

  return (
    <>
      <div class="flex min-w-0 items-center gap-2">
        <span class="truncate text-xs text-muted-foreground">{project()}</span>
        <span class="text-muted-foreground">/</span>
        <span class="truncate text-xs text-muted-foreground">actions</span>
        <span class="text-muted-foreground">/</span>
        <span class="truncate font-semibold">{actionName()}</span>
        <Show when={status() !== "idle"}>
          <Badge variant={statusTone(status())}>
            {statusLabel(status(), exitCode())}
          </Badge>
        </Show>
        <Show when={act()}>
          {(a) => (
            <span class="hidden truncate text-xs text-muted-foreground font-mono md:inline">
              {a().command}
            </span>
          )}
        </Show>
        <Show when={pid() > 0}>
          <span class="text-xs text-muted-foreground font-mono">
            pid {pid()}
          </span>
        </Show>
      </div>
      <div class="ml-auto flex items-center gap-1.5">
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant={isPreviousLogs({ kind: "action", project: project(), action: actionName() }) ? "secondary" : "outline"}
            size="sm"
            onClick={() => togglePreviousLogs({ kind: "action", project: project(), action: actionName() })}
          >
            <History class="size-4" />
            <span class="hidden md:inline">
              {isPreviousLogs({ kind: "action", project: project(), action: actionName() }) ? "Live" : "Previous"}
            </span>
          </TooltipTrigger>
          <TooltipContent>Show previous run's logs (p)</TooltipContent>
        </Tooltip>
        <Button
          size="sm"
          variant="default"
          onClick={() => runAction(project(), actionName())}
          class="gap-1.5"
        >
          <Play class="size-3.5" />
          Run Action
        </Button>
      </div>
    </>
  );
}

function EmptyState() {
  const project = () => selectedProject();
  const actionList = createMemo(() => (project() ? actions()[project()!] ?? [] : []));

  return (
    <div class="flex h-full flex-col items-center justify-center gap-4 text-muted-foreground p-6">
      <SquareTerminal class="size-10" stroke-width={1} />
      <Show
        when={project()}
        fallback={<p class="text-sm">Select a project from the sidebar.</p>}
      >
        <div class="max-w-md w-full text-center">
          <p class="text-sm leading-relaxed mb-2">
            Select a service under <span class="font-semibold text-foreground">{project()}</span> to view its logs.
          </p>
          <Show when={actionList().length > 0}>
            <div class="mt-4 border border-border rounded-lg p-4 bg-card text-card-foreground text-left shadow-sm">
              <div class="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
                Project Actions
              </div>
              <div class="grid gap-2">
                <For each={actionList()}>
                  {(act) => (
                    <div class="flex items-center justify-between p-2.5 rounded-md border border-border/60 bg-muted/30 hover:bg-muted/60 transition-colors">
                      <div class="min-w-0 pr-2">
                        <div class="font-medium text-sm text-foreground truncate">{act.name}</div>
                        <div class="text-xs text-muted-foreground font-mono mt-0.5 truncate">{act.command}</div>
                      </div>
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() => runAction(project()!, act.name)}
                        class="gap-1.5 shrink-0"
                      >
                        <Play class="size-3.5 text-primary" />
                        Run
                      </Button>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export function Main() {
  return (
    <main class="flex h-full min-w-0 flex-1 flex-col bg-background overflow-hidden">
      <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
        <Show
          when={selectedProject()}
          fallback={<span class="font-semibold">Welcome</span>}
        >
          <Show
            when={selectedService()}
            fallback={
              <Show when={selectedAction()} fallback={<ProjectHeader />}>
                <ActionHeader />
              </Show>
            }
          >
            <ServiceHeader />
          </Show>
        </Show>
      </header>

      {/* Main Upper Area: Logs are ALWAYS visible when a service or project is selected */}
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <Show
          when={activeView() !== "git"}
          fallback={<GitView />}
        >
          <Show
            when={selectedService()}
            fallback={
              <Show when={selectedAction()} fallback={<EmptyState />}>
                <ActionLogViewer />
              </Show>
            }
          >
            <LogViewer />
          </Show>
        </Show>
      </div>
      {/* Bottom Panel for Shell/Git */}
      <BottomPanel />
    </main>
  );
}
