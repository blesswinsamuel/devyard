import { For, Show, createEffect, createSignal, onCleanup, onMount } from "solid-js";
import { ArrowDownToLine, History, RadioTower, X } from "lucide-solid";
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
import { actions as actionsMap, services as servicesMap } from "~/stores/data";
import {
  selectedAction,
  selectedProject,
  selectedService,
  selectService,
  selectAction,
} from "~/stores/nav";
import { theme } from "~/stores/app";
import { subscribeActionLogs, subscribeLogs } from "~/lib/ws";
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
  kind: "service" | "action";
  target: string;
  active: boolean;
  keyId: string;
}) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;

  const [receivedAny, setReceivedAny] = createSignal(false);
  const [following, setFollowing] = createSignal(true);

  onMount(() => {
    const t = createTerminal(container, theme());
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

    // Re-subscribe reactively when the previous/live mode changes; either way
    // the stream restarts from history.
    createEffect(() => {
      if (!props.active) return;
      const prev = isPreviousLogs(props.keyId);
      resetView();
      const subscribe = props.kind === "service" ? subscribeLogs : subscribeActionLogs;
      const unsub = subscribe(
        props.project,
        props.target,
        (line) => {
          setReceivedAny(true);
          buffer.push("\x1b[0m" + formatLogLine(line));
          scheduleFlush();
        },
        resetView,
        prev
      );
      onCleanup(() => {
        unsub();
        resetView();
      });
    });

    onCleanup(() => {
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
        <div class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground">
          <Spinner class="opacity-60" />
          <p class="text-xs">{isPreviousLogs(props.keyId) ? "Loading previous run…" : "Waiting for output…"}</p>
        </div>
      </Show>
      <Show when={receivedAny() && !following()}>
        <Button
          variant="outline"
          size="sm"
          onClick={jumpToBottom}
          class="absolute bottom-4 right-5 rounded-full border-border-strong bg-popover/95 text-xs text-muted-foreground shadow-md backdrop-blur transition-colors hover:text-foreground"
        >
          <ArrowDownToLine class="!size-3.5" />
          Jump to latest
        </Button>
      </Show>
    </div>
  );
}

export function LogView() {
  // Open/focus a tab whenever the selection targets a service or action.
  createEffect(() => {
    const p = selectedProject();
    const svc = selectedService();
    const act = selectedAction();
    if (p && svc) openLogTab(p, "service", svc);
    else if (p && act) openLogTab(p, "action", act);
  });

  // Prune tabs whose targets vanished.
  createEffect(() => {
    pruneLogTabs(allValidKeys());
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
                    title={tab.kind === "action" ? `${tab.project} · action` : `${tab.project} · service`}
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

function allValidKeys(): Set<string> {
  const valid = new Set<string>();
  for (const [project, list] of Object.entries(servicesMap())) {
    for (const s of list) valid.add(tabKey(project, "service", s.name));
  }
  for (const [project, list] of Object.entries(actionsMap())) {
    for (const a of list) valid.add(tabKey(project, "action", a.name));
  }
  return valid;
}

/** Clicking a tab re-selects that service/action everywhere (sidebar + route). */
function focusTab(tab: { project: string; kind: "service" | "action"; name: string }) {
  if (tab.kind === "service") selectService(tab.project, tab.name);
  else selectAction(tab.project, tab.name);
}

function LogEmptyState() {
  return (
    <Empty class="h-full border-0">
      <EmptyHeader>
        <EmptyMedia>
          <RadioTower class="size-8 stroke-1 text-muted-foreground" />
        </EmptyMedia>
        <EmptyTitle class="text-sm">No log streams open</EmptyTitle>
        <EmptyDescription class="max-w-xs text-xs leading-relaxed">
          Select a service in the sidebar to follow its logs. Tabs stay open across
          projects — close them with ×.
        </EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}
