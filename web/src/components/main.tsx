import { createEffect, createMemo, createSignal, onCleanup, onMount, Show } from "solid-js";
import type { Terminal } from "@xterm/xterm";
import { Power, RotateCcw, Skull, SquareTerminal } from "lucide-solid";
import {
  projects,
  services,
  selectedProject,
  selectedService,
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
import { createTerminal, terminalTheme } from "~/terminal";
import { formatLogLine } from "~/lib/ansi";

function LogViewer() {
  let container!: HTMLDivElement;
  const [term, setTerm] = createSignal<Terminal | null>(null);

  onMount(() => {
    const t = createTerminal(container);
    setTerm(t);
    onCleanup(() => {
      t.dispose();
      setTerm(null);
    });
  });

  createEffect(() => {
    const project = selectedProject();
    const service = selectedService();
    const t = term();
    if (!project || !service || !t) return;

    let buffer: string[] = [];
    let rafId: number | null = null;

    const flush = () => {
      if (buffer.length > 0) {
        t.write(buffer.join("\r\n") + "\r\n");
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

    const unsubscribe = subscribeLogs(
      project,
      service,
      // Reset SGR before each line so an unclosed color from a prior line
      // doesn't tint timestamps / following content. Clean non-SGR ANSI the
      // same way the CLI and TUI do.
      (line) => {
        buffer.push("\x1b[0m" + formatLogLine(line));
        scheduleFlush();
      },
      resetView
    );

    return () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      buffer = [];
      unsubscribe();
    };
  });

  createEffect(() => {
    const t = term();
    if (t) t.options.theme = terminalTheme(theme());
  });

  return (
    <div ref={container} class="h-full w-full overflow-hidden bg-background" />
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
            variant="outline"
            size="sm"
            onClick={() => stopProject(project())}
          >
            <Power />
            Stop project
          </TooltipTrigger>
          <TooltipContent>Stop project (d)</TooltipContent>
        </Tooltip>
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

      <div class="relative min-h-0 flex-1">
        <Show when={selectedService()} fallback={<EmptyState />}>
          <LogViewer />
        </Show>
      </div>
    </main>
  );
}
