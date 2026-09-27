import { Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { ArrowDownToLine, History, Play, RadioTower } from "lucide-solid";
import { runTask, services as servicesMap, tasks as tasksMap } from "~/stores/data";
import { subscribeLogs, subscribeTaskLogs } from "~/stores/logs";
import { useColorMode } from "~/components/color-mode";
import { formatLogLine } from "~/lib/ansi";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { togglePreviousLogs } from "~/stores/workspace";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import type { ServiceLogTab, TaskLogTab } from "~/lib/types";

export type LogTab = ServiceLogTab | TaskLogTab;

/**
 * One log stream rendered into an xterm instance. Follow-tail behavior:
 * auto-scrolls while pinned to the bottom; scrolling up unpins and shows a
 * jump-to-latest affordance until the user returns.
 */
export function LogContent(props: { tab: LogTab; active: boolean }) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;

  const [receivedAny, setReceivedAny] = createSignal(false);
  const [following, setFollowing] = createSignal(true);
  const [notFound, setNotFound] = createSignal(false);

  const kind = () => (props.tab.kind === "log-service" ? "service" : "task");
  const target = () =>
    props.tab.kind === "log-service"
      ? (props.tab as ServiceLogTab).service
      : (props.tab as TaskLogTab).task;
  const isPrevious = () => !!props.tab.previous;

  const task = createMemo(() =>
    props.tab.kind === "log-task"
      ? (tasksMap()[props.tab.project] ?? []).find((t) => t.name === (props.tab as TaskLogTab).task)
      : undefined
  );
  const isRunning = createMemo(() => {
    if (props.tab.kind === "log-task") {
      const s = task()?.status;
      return s === "running" || s === "starting";
    }
    const svc = (servicesMap()[props.tab.project] ?? []).find(
      (s) => s.name === (props.tab as ServiceLogTab).service
    );
    return svc?.status === "running" || svc?.status === "starting";
  });

  const { colorMode } = useColorMode();

  onMount(() => {
    const t = createTerminal(container, colorMode(), {
      convertEol: true,
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: "bar",
    });
    term = t;

    let buffer: string[] = [];
    let rafId: number | null = null;
    // xterm throws if written to or scrolled after dispose; tab rows remount
    // when their tab object is replaced, so pending callbacks must no-op.
    let disposed = false;

    const flush = () => {
      if (buffer.length > 0 && !disposed) {
        const atBottom = following();
        t.write(buffer.join("\r\n") + "\r\n", () => {
          if (atBottom && !disposed) t.scrollToBottom();
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
      if (disposed) return;
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
      t.options.theme = terminalTheme(colorMode());
    });

    let unsub: (() => void) | null = null;

    const startStream = () => {
      unsub?.();
      resetView();
      const prev = isPrevious();
      const subscribe = props.tab.kind === "log-service" ? subscribeLogs : subscribeTaskLogs;
      const name = target();
      unsub = subscribe(
        props.tab.project,
        name,
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

    // Keep the stream subscribed for the tab's lifetime (like terminals do),
    // so switching tabs or moving them between panes never shows a
    // "waiting for output" gap. Re-subscribe only when the source changes:
    // previous/live mode, or a task transitioning to running.
    createEffect(() => {
      isPrevious();
      startStream();
    });

    // Re-subscribe when an idle/unrun task transitions to running.
    createEffect((prevRunning?: boolean) => {
      const running = isRunning();
      if (prevRunning === false && running === true) {
        startStream();
      }
      return running;
    });

    onCleanup(() => {
      disposed = true;
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
      requestAnimationFrame(() => {
        term?.fit();
        // Streams may have written while the tab was hidden; force a repaint.
        term?.refresh(0, term.rows - 1);
      });
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
            when={isPrevious()}
            fallback={
              <Show
                when={notFound() && kind() === "task" && !isRunning()}
                fallback={
                  <Show
                    when={notFound() && kind() === "service"}
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
                        <div class="flex max-w-xs flex-col gap-1">
                          <p class="text-xs text-muted-foreground">
                            No logs found for service &ldquo;{target()}&rdquo;.
                          </p>
                        </div>
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
                    onClick={() => runTask(props.tab.project, target())}
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
                    There are no archived logs from an earlier run of {kind()} &ldquo;{target()}&rdquo;.
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => togglePreviousLogs(props.tab.id)}
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
