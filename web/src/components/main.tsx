import { For, createEffect, createMemo, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Bot, FileText, GitBranch, Play, Power, RotateCcw, Skull, SquareTerminal } from "lucide-solid";
import {
  projects,
  services,
  selectedProject,
  selectedService,
  activeView,
  setActiveView,
  startProject,
  stopProject,
  restartService,
  stopService,
  killService,
  theme,
} from "~/store";
import { subscribeLogs } from "~/ws";
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
import { ShellWorkspace } from "~/components/shell_workspace";

function ServiceTerminal(props: {
  project: string;
  service: string;
  active: boolean;
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
      t.clear();
      t.write("\x1b[2J\x1b[3J\x1b[H");
    };

    resetView();

    const unsubLogs = subscribeLogs(
      props.project,
      props.service,
      // Reset SGR before each line so an unclosed color from a prior line
      // doesn't tint timestamps / following content. Clean non-SGR ANSI the
      // same way the CLI and TUI do.
      (line) => {
        buffer.push("\x1b[0m" + formatLogLine(line));
        scheduleFlush();
      },
      resetView
    );

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    onCleanup(() => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
      }
      unsubLogs();
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

function LogViewer() {
  const [activeServices, setActiveServices] = createSignal<
    { project: string; service: string; key: string }[]
  >([]);

  const activeKey = () => {
    const p = selectedProject();
    const s = selectedService();
    return p && s ? `${p}/${s}` : null;
  };

  createEffect(() => {
    const p = selectedProject();
    const s = selectedService();
    if (!p || !s) return;
    const key = `${p}/${s}`;
    setActiveServices((prev) => {
      const filtered = prev.filter((item) => item.project === p);
      if (!filtered.some((item) => item.key === key)) {
        return [...filtered, { project: p, service: s, key }];
      }
      return filtered;
    });
  });

  return (
    <div class="relative h-full w-full">
      <For each={activeServices()}>
        {(item) => (
          <ServiceTerminal
            project={item.project}
            service={item.service}
            active={activeKey() === item.key}
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
              onClick={() => restartService(project(), service())}
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
          <Tooltip>
            <TooltipTrigger
              as={Button}
              variant="outline"
              size="sm"
              onClick={() => stopProject(project())}
            >
              <Power />
              Stop project
            </TooltipTrigger>
            <TooltipContent>Stop project (d)</TooltipContent>
          </Tooltip>
        </Show>
      </div>
    </>
  );
}

function EmptyState() {
  return (
    <div class="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
      <SquareTerminal class="size-10" stroke-width={1} />
      <Show
        when={selectedProject()}
        fallback={<p class="text-sm">Select a project from the sidebar.</p>}
      >
        <p class="max-w-sm text-center text-sm leading-relaxed">
          Select a service under{" "}
          <span class="text-foreground">{selectedProject()}</span> to view its
          logs.
        </p>
      </Show>
    </div>
  );
}

function ViewTabs() {
  return (
    <div class="flex items-center gap-1 border-b border-border bg-muted/20 px-4 py-1 text-xs">
      <button
        class="flex items-center gap-1.5 rounded px-2.5 py-1 transition-colors cursor-pointer"
        classList={{
          "bg-background font-medium text-foreground shadow-sm": activeView() === "logs",
          "text-muted-foreground hover:text-foreground": activeView() !== "logs",
        }}
        onClick={() => setActiveView("logs")}
      >
        <FileText class="size-3.5" />
        <span>Logs</span>
      </button>

      <button
        class="flex items-center gap-1.5 rounded px-2.5 py-1 transition-colors cursor-pointer"
        classList={{
          "bg-background font-medium text-foreground shadow-sm": activeView() === "shell",
          "text-muted-foreground hover:text-foreground": activeView() !== "shell",
        }}
        onClick={() => setActiveView("shell")}
      >
        <SquareTerminal class="size-3.5" />
        <span>Shell</span>
      </button>

      <Tooltip>
        <TooltipTrigger
          as="button"
          disabled
          class="flex cursor-not-allowed items-center gap-1.5 rounded px-2.5 py-1 text-muted-foreground/50 opacity-60"
        >
          <GitBranch class="size-3.5" />
          <span>Git</span>
          <span class="rounded bg-muted px-1 text-[10px]">Soon</span>
        </TooltipTrigger>
        <TooltipContent>Git graph, diffs & commits coming soon!</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          as="button"
          disabled
          class="flex cursor-not-allowed items-center gap-1.5 rounded px-2.5 py-1 text-muted-foreground/50 opacity-60"
        >
          <Bot class="size-3.5" />
          <span>Agents</span>
          <span class="rounded bg-muted px-1 text-[10px]">Soon</span>
        </TooltipTrigger>
        <TooltipContent>AI Coding agents coming soon!</TooltipContent>
      </Tooltip>
    </div>
  );
}

export function Main() {
  return (
    <main class="flex h-full min-w-0 flex-1 flex-col bg-background">
      <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
        <Show
          when={selectedProject()}
          fallback={<span class="font-semibold">Welcome</span>}
        >
          <Show when={selectedService()} fallback={<ProjectHeader />}>
            <ServiceHeader />
          </Show>
        </Show>
      </header>

      <Show when={selectedProject()}>
        <ViewTabs />
      </Show>

      <div class="relative min-h-0 flex-1">
        <Show
          when={activeView() === "shell"}
          fallback={
            <Show when={selectedService()} fallback={<EmptyState />}>
              <LogViewer />
            </Show>
          }
        >
          <ShellWorkspace />
        </Show>
      </div>
    </main>
  );
}
