import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { ArrowDownToLine, History, Play, RadioTower, X } from "lucide-solid";
import {
  activeKey,
  closeLogTab,
  isPreviousLogs,
  openLogTab,
  pruneLogTabs,
  tabs,
  tabKey,
  togglePreviousLogs,
} from "~/stores/logs";
import { projects as projectsList, runTask, services as servicesMap, tasks as tasksMap } from "~/stores/data";
import {
  selectedProject,
  selectedService,
  selectedTask,
  selectService,
  selectTask,
} from "~/stores/nav";
import { theme } from "~/stores/app";
import { subscribeLogs, subscribeTaskLogs } from "~/stores/logs";
import { formatLogLine } from "~/lib/ansi";
import { cn } from "~/lib/utils";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { Button } from "~/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { Spinner } from "~/components/ui/spinner";

/**
 * One log stream rendered into an xterm instance. Follow-tail behavior:
 * auto-scrolls while pinned to the bottom; scrolling up unpins and shows a
 * jump-to-latest affordance until the user returns.
 */
function LogTerminal(props: {
  project: string;
  kind: "service" | "task";
  target: string;
  active: boolean;
  keyId: string;
}) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;

  const [receivedAny, setReceivedAny] = createSignal(false);
  const [following, setFollowing] = createSignal(true);
  const [notFound, setNotFound] = createSignal(false);

  const task = createMemo(() =>
    props.kind === "task"
      ? (tasksMap()[props.project] ?? []).find((t) => t.name === props.target)
      : undefined
  );
  const isRunning = createMemo(() => {
    if (props.kind === "task") {
      const s = task()?.status;
      return s === "running" || s === "starting";
    }
    const svc = (servicesMap()[props.project] ?? []).find((s) => s.name === props.target);
    return svc?.status === "running" || svc?.status === "starting";
  });

  onMount(() => {
    const t = createTerminal(container, theme(), {
      convertEol: true,
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: "bar",
    });
    term = t;

    let buffer: string[] = [];
    let rafId: number | null = null;

    const flush = () => {
      if (buffer.length > 0) {
        const atBottom = following();
        t.write(buffer.join("\r\n") + "\r\n", () => {
          if (atBottom) t.scrollToBottom();
        });
        buffer = [];
      }
      rafId = null;
    };
    const scheduleFlush = () => {
      if (rafId === null) rafId = requestAnimationFrame(flush);
    };

    const resetView = () => {
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      buffer = [];
      setReceivedAny(false);
      setNotFound(false);
      setFollowing(true);
      t.reset();
      t.clear();
      t.write("\x1b[2J\x1b[3J\x1b[H");
    };

    // Track whether the user is pinned to the bottom of the scrollback.
    const handleScroll = (e: Event) => {
      const vp = e.target as HTMLElement;
      if (!vp.classList.contains("xterm-viewport")) return;
      setFollowing(vp.scrollTop + vp.clientHeight >= vp.scrollHeight - 8);
    };
    container.addEventListener("scroll", handleScroll, true);

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    let unsub: (() => void) | null = null;

    const startStream = () => {
      unsub?.();
      resetView();
      const prev = isPreviousLogs(props.keyId);
      const subscribe = props.kind === "service" ? subscribeLogs : subscribeTaskLogs;
      unsub = subscribe(
        props.project,
        props.target,
        (line) => {
          setReceivedAny(true);
          buffer.push("\x1b[0m" + formatLogLine(line));
          scheduleFlush();
        },
        resetView,
        prev,
        () => {
          setNotFound(true);
        }
      );
    };

    // Re-subscribe reactively when active or previous/live mode changes.
    createEffect(() => {
      if (!props.active) {
        unsub?.();
        unsub = null;
        resetView();
        return;
      }
      isPreviousLogs(props.keyId);
      startStream();
    });

    // Re-subscribe when an idle/unrun task transitions to running.
    createEffect((prevRunning?: boolean) => {
      const running = isRunning();
      if (props.active && prevRunning === false && running === true) {
        startStream();
      }
      return running;
    });

    onCleanup(() => {
      unsub?.();
      unsub = null;
      container.removeEventListener("scroll", handleScroll, true);
      if (rafId !== null) cancelAnimationFrame(rafId);
      t.dispose();
      term = null;
    });
  });

  createEffect(() => {
    if (props.active && term) {
      requestAnimationFrame(() => term?.fit());
    }
  });

  const jumpToBottom = () => {
    const vp = container.querySelector<HTMLElement>(".xterm-viewport");
    if (vp) vp.scrollTop = vp.scrollHeight;
    term?.scrollToBottom();
    setFollowing(true);
  };

  return (
    <div class="relative h-full w-full overflow-hidden bg-[#101014] dark:bg-transparent" classList={{ hidden: !props.active }}>
      <div ref={container} class="h-full w-full" />
      <Show when={!receivedAny()}>
        <div class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 p-6 text-muted-foreground">
          <Show
            when={isPreviousLogs(props.keyId)}
            fallback={
              <Show
                when={notFound() && props.kind === "task" && !isRunning()}
                fallback={
                  <Show
                    when={notFound() && props.kind === "service"}
                    fallback={
                      <div class="flex flex-col items-center gap-2">
                        <Spinner class="opacity-60" />
                        <p class="text-xs">Waiting for output…</p>
                      </div>
                    }
                  >
                    <div class="pointer-events-auto flex flex-col items-center gap-3 text-center">
                      <div class="flex size-10 items-center justify-center rounded-full bg-muted/60 text-muted-foreground">
                        <RadioTower class="size-5" />
                      </div>
                      <div class="flex flex-col gap-1">
                        <p class="text-sm font-medium text-foreground">No logs available</p>
                        <p class="max-w-xs text-xs text-muted-foreground">
                          No logs found for service &ldquo;{props.target}&rdquo;.
                        </p>
                      </div>
                    </div>
                  </Show>
                }
              >
                <div class="pointer-events-auto flex flex-col items-center gap-3 text-center">
                  <div class="flex size-10 items-center justify-center rounded-full bg-muted/60 text-muted-foreground">
                    <Play class="size-5 translate-x-0.5" />
                  </div>
                  <div class="flex flex-col gap-1">
                    <p class="text-sm font-medium text-foreground">Task has not run yet</p>
                    <p class="max-w-xs text-xs text-muted-foreground">
                      Run this task to start execution and view its output stream.
                    </p>
                  </div>
                  <Show when={task()?.command}>
                    <code class="rounded border border-border/60 bg-muted/40 px-2.5 py-1 font-mono text-xs text-foreground/80">
                      {task()?.command}
                    </code>
                  </Show>
                  <Button
                    size="sm"
                    onClick={() => runTask(props.project, props.target)}
                    class="mt-1 gap-1.5"
                  >
                    <Play class="!size-3.5" />
                    Run task
                  </Button>
                </div>
              </Show>
            }
          >
            <Show
              when={notFound()}
              fallback={
                <div class="flex flex-col items-center gap-2">
                  <Spinner class="opacity-60" />
                  <p class="text-xs">Loading previous run…</p>
                </div>
              }
            >
              <div class="pointer-events-auto flex flex-col items-center gap-3 text-center">
                <div class="flex size-10 items-center justify-center rounded-full bg-muted/60 text-muted-foreground">
                  <History class="size-5" />
                </div>
                <div class="flex flex-col gap-1">
                  <p class="text-sm font-medium text-foreground">No previous run logs</p>
                  <p class="max-w-xs text-xs text-muted-foreground">
                    There are no archived logs from an earlier run of {props.kind} &ldquo;{props.target}&rdquo;.
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => togglePreviousLogs(props.keyId)}
                  class="mt-1"
                >
                  Switch to live logs
                </Button>
              </div>
            </Show>
          </Show>
        </div>
      </Show>
      <Show when={receivedAny() && !following()}>
        <Button
          variant="outline"
          size="sm"
          onClick={jumpToBottom}
          class="absolute bottom-4 right-5 rounded-full border-border-strong bg-popover/95 text-muted-foreground shadow-md backdrop-blur"
        >
          <ArrowDownToLine class="!size-3.5" />
          Jump to latest
        </Button>
      </Show>
    </div>
  );
}

export function LogView() {
  // Open/focus a tab whenever the selection targets a service or task.
  createEffect(() => {
    const p = selectedProject();
    const svc = selectedService();
    const task = selectedTask();
    if (p && svc) openLogTab(p, "service", svc);
    else if (p && task) openLogTab(p, "task", task);
  });

  // Prune tabs whose targets vanished.
  createEffect(() => {
    const projs = projectsList();
    const svcs = servicesMap();
    const tasks = tasksMap();

    pruneLogTabs((tab) => {
      if (projs.length > 0 && !projs.some((p) => p.name === tab.project)) {
        return false;
      }
      if (tab.kind === "service") {
        const list = svcs[tab.project];
        if (list !== undefined && !list.some((s) => s.name === tab.name)) {
          return false;
        }
      }
      if (tab.kind === "task") {
        const list = tasks[tab.project];
        if (list !== undefined && !list.some((a) => a.name === tab.name)) {
          return false;
        }
      }
      return true;
    });
  });

  return (
    <div class="flex h-full min-w-0 flex-col">
      <Show when={tabs().length > 0}>
        <div class="flex h-9 shrink-0 items-center gap-1 overflow-x-auto border-b bg-card px-2">
          <For each={tabs()}>
            {(tab) => {
              const isActive = () => activeKey() === tab.key;
              return (
                <div
                  class={cn(
                    "group flex h-6 shrink-0 items-stretch overflow-hidden rounded-md border text-xs transition-colors",
                    isActive()
                      ? "border-accent-foreground/10 bg-accent text-accent-foreground"
                      : "border-transparent text-muted-foreground hover:bg-muted hover:text-foreground"
                  )}
                >
                  <button
                    type="button"
                    onClick={() => focusTab(tab)}
                    class="flex min-w-0 items-center gap-1.5 px-2 focus-visible:outline-none"
                    title={tab.kind === "task" ? `${tab.project} · task` : `${tab.project} · service`}
                  >
                    <span
                      class={cn("size-1.5 shrink-0 rounded-full", tab.kind === "service" ? "bg-primary" : "bg-warning")}
                    />
                    <span class="truncate">
                      <Show when={selectedProject() !== tab.project}>
                        <span class="text-muted-foreground/70">{tab.project}/</span>
                      </Show>
                      {tab.name}
                    </span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Close ${tab.name}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      closeLogTab(tab.key);
                    }}
                    class={cn(
                      "flex w-5 items-center justify-center text-muted-foreground/60 transition-colors hover:bg-muted hover:text-foreground",
                      !isActive() && "opacity-0 group-hover:opacity-100"
                    )}
                  >
                    <X class="size-3" />
                  </button>
                </div>
              );
            }}
          </For>

          {/* Active-tab previous/live switch */}
          <Show when={activeKey()} keyed>
            {(key: string) => (
              <button
                type="button"
                onClick={() => togglePreviousLogs(key)}
                class={cn(
                  "ml-auto flex h-6 shrink-0 items-center gap-1.5 rounded-md border px-2 text-[11px] transition-colors",
                  "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                  isPreviousLogs(key)
                    ? "border-warning/50 bg-warning/10 text-warning"
                    : "border-input text-muted-foreground hover:bg-muted hover:text-foreground"
                )}
                title="Toggle previous run's logs (p)"
              >
                <History class="size-3" />
                {isPreviousLogs(key) ? "previous run" : "live"}
              </button>
            )}
          </Show>
        </div>
      </Show>

      <div class="relative min-h-0 flex-1">
        <Show when={tabs().length > 0} fallback={<LogEmptyState />}>
          <For each={tabs()}>
            {(tab) => (
              <LogTerminal
                project={tab.project}
                kind={tab.kind}
                target={tab.name}
                keyId={tab.key}
                active={activeKey() === tab.key}
              />
            )}
          </For>
        </Show>
      </div>
    </div>
  );
}

/** Clicking a tab re-selects that service/task everywhere (sidebar + route). */
function focusTab(tab: { project: string; kind: "service" | "task"; name: string }) {
  if (tab.kind === "service") selectService(tab.project, tab.name);
  else selectTask(tab.project, tab.name);
}

function LogEmptyState() {
  return (
    <Empty class="h-full border-0">
      <EmptyHeader>
        <EmptyMedia>
          <RadioTower class="size-8 stroke-1 text-muted-foreground" />
        </EmptyMedia>
        <EmptyTitle>No log streams open</EmptyTitle>
        <EmptyDescription class="max-w-xs">
          Select a service in the sidebar to follow its logs. Tabs stay open across
          projects — close them with ×.
        </EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}
