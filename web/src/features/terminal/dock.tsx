import { createMemo, createSignal, For, Match, onCleanup, Show, Switch } from "solid-js";
import { Columns2, Maximize2, Minimize2, PanelBottomClose, Plug, Plus, ScrollText, SquareTerminal, X } from "lucide-solid";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { dock, dockActions, DOCK_MIN_HEIGHT, type DockTab } from "~/data/dock";
import { getTask, projectList, servicesOf } from "~/data/entities";
import { isMobile } from "~/lib/media";
import { cn } from "~/lib/utils";
import { routeTarget } from "~/app/runtime";
import { LogViewer } from "~/features/logs/log-viewer";
import { SessionView } from "./lazy";

const ICONS = { terminal: SquareTerminal, attach: Plug, logs: ScrollText } as const;

function TabContent(props: { tab: DockTab; active: boolean }) {
  return (
    <Switch>
      <Match when={props.tab.kind === "terminal" && props.tab}>
        {(tab) => (
          <SessionView
            class="h-full"
            target={{ kind: "terminal", project: tab().project, sessionId: tab().sessionId }}
            active={props.active}
            onSessionId={(id) => dockActions.setSessionId(tab().id, id)}
            endActions={() => (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  dockActions.close(tab().id);
                  dockActions.openTerminal(tab().project);
                }}
              >
                New terminal
              </Button>
            )}
          />
        )}
      </Match>
      <Match when={props.tab.kind === "attach" && props.tab}>
        {(tab) => (
          <SessionView
            class="h-full"
            target={{ kind: tab().target, project: tab().project, name: tab().name }}
            active={props.active}
            endActions={() => (
              <Show when={tab().target === "task" && getTask(tab().project, tab().name)}>
                <Button size="sm" variant="outline" onClick={() => dockActions.close(tab().id)}>
                  Close tab
                </Button>
              </Show>
            )}
          />
        )}
      </Match>
      <Match when={props.tab.kind === "logs" && props.tab}>
        {(tab) => (
          <LogViewer
            class="h-full rounded-none border-0"
            label={`${tab().title} logs`}
            project={tab().project}
            sources={tab().sources}
            chipSources={
              tab().sources.length
                ? undefined
                : servicesOf(tab().project).map((s) => ({ kind: "service" as const, name: s.name }))
            }
          />
        )}
      </Match>
    </Switch>
  );
}

function TabStrip(props: { onMaximize?: () => void; maximized?: boolean }) {
  const currentProject = () => {
    const t = routeTarget();
    return t.kind === "app" ? projectList()[0]?.id : t.project;
  };
  const activeId = () => dock.panes[dock.focused];
  let strip!: HTMLDivElement;

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    const tabs = dock.tabs;
    const idx = tabs.findIndex((t) => t.id === activeId());
    const next = tabs[(idx + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
    if (!next) return;
    e.preventDefault();
    dockActions.activate(next.id);
    strip.querySelector<HTMLElement>(`[data-tab-id="${next.id}"]`)?.focus();
  };

  return (
    <div class="flex h-9 shrink-0 items-center gap-1 border-b bg-card px-1.5">
      <div ref={strip} class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto no-scrollbar" role="tablist" aria-label="Dock tabs" onKeyDown={onKeyDown}>
        <For each={dock.tabs}>
          {(tab) => {
            const Icon = ICONS[tab.kind];
            const selected = () => dock.panes[0] === tab.id || dock.panes[1] === tab.id;
            return (
              <div
                class={cn(
                  "group flex h-7 shrink-0 items-center rounded-md border border-transparent text-ui",
                  selected() ? "border-border bg-background text-foreground" : "text-muted-foreground hover:bg-muted",
                  activeId() === tab.id && "border-primary/40",
                )}
              >
                <button
                  type="button"
                  role="tab"
                  data-tab-id={tab.id}
                  aria-selected={selected()}
                  tabindex={activeId() === tab.id ? 0 : -1}
                  class="focus-ring flex h-full items-center gap-1.5 rounded-md pr-1 pl-2"
                  onClick={() => dockActions.activate(tab.id)}
                  onAuxClick={(e) => e.button === 1 && dockActions.close(tab.id)}
                  title={`${tab.project} · ${tab.title}`}
                >
                  <Icon class="size-3.5 shrink-0" />
                  <span class="max-w-40 truncate">{tab.title}</span>
                </button>
                <button
                  type="button"
                  class="focus-ring mr-1 rounded-sm p-0.5 opacity-60 hover:bg-muted hover:opacity-100"
                  aria-label={`Close ${tab.title}`}
                  onClick={() => dockActions.close(tab.id)}
                >
                  <X class="size-3" />
                </button>
              </div>
            );
          }}
        </For>
        <DropdownMenu>
          <DropdownMenuTrigger as={Button} variant="ghost" size="icon-xs" aria-label="New terminal">
            <Plus />
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuLabel>New terminal in…</DropdownMenuLabel>
            <For each={projectList().filter((p) => !p.error)}>
              {(p) => (
                <DropdownMenuItem onSelect={() => dockActions.openTerminal(p.id)}>
                  <SquareTerminal />
                  {p.id}
                  <Show when={p.id === currentProject()}>
                    <span class="ml-auto text-2xs text-muted-foreground">current</span>
                  </Show>
                </DropdownMenuItem>
              )}
            </For>
            <Show when={projectList().length === 0}>
              <DropdownMenuItem disabled>No projects</DropdownMenuItem>
            </Show>
            <DropdownMenuSeparator />
            <For each={projectList()}>
              {(p) => (
                <DropdownMenuItem onSelect={() => dockActions.pinLogs(p.id, [], `${p.id} · all`)}>
                  <ScrollText />
                  Pin {p.id} logs
                </DropdownMenuItem>
              )}
            </For>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <Show when={dock.tabs.length > 1 && !isMobile()}>
        <Button
          variant={dock.panes[1] ? "secondary" : "ghost"}
          size="icon-xs"
          aria-label={dock.panes[1] ? "Unsplit" : "Split"}
          aria-pressed={!!dock.panes[1]}
          title={dock.panes[1] ? "Unsplit" : "Split: show two tabs side by side"}
          onClick={() => dockActions.toggleSplit()}
        >
          <Columns2 />
        </Button>
      </Show>
      <Show when={props.onMaximize}>
        <Button variant="ghost" size="icon-xs" aria-label={props.maximized ? "Restore dock" : "Maximize dock"} onClick={props.onMaximize}>
          <Show when={props.maximized} fallback={<Maximize2 />}>
            <Minimize2 />
          </Show>
        </Button>
      </Show>
      <Button variant="ghost" size="icon-xs" aria-label="Hide dock" title="Hide dock (⌘J)" onClick={() => dockActions.setOpen(false)}>
        <PanelBottomClose />
      </Button>
    </div>
  );
}

/** Keeps every tab mounted (sessions and pinned streams stay live); the
 * tabs assigned to panes are placed into grid columns, the rest are hidden. */
function DockPanes() {
  const split = () => !!dock.panes[1];
  return (
    <div class={cn("grid min-h-0 flex-1", split() ? "grid-cols-2" : "grid-cols-1")}>
      <For each={dock.tabs}>
        {(tab) => {
          const pane = createMemo(() => (dock.panes[0] === tab.id ? 0 : dock.panes[1] === tab.id ? 1 : -1));
          return (
            <div
              class={cn(
                "min-h-0 min-w-0",
                pane() < 0 && "hidden",
                pane() === 1 && "border-l",
                split() && dock.focused === pane() && "ring-1 ring-primary/40 ring-inset",
              )}
              style={{ "grid-column": `${Math.max(0, pane()) + 1}`, "grid-row": "1" }}
              role="tabpanel"
              aria-label={tab.title}
              onPointerDown={() => pane() >= 0 && dockActions.focusPane(pane() as 0 | 1)}
              onFocusIn={() => pane() >= 0 && dockActions.focusPane(pane() as 0 | 1)}
            >
              <TabContent tab={tab} active={dock.open && pane() >= 0} />
            </div>
          );
        }}
      </For>
    </div>
  );
}

/** Desktop: resizable bottom panel. Mobile: full-screen sheet. */
export function Dock() {
  const [maximized, setMaximized] = createSignal(false);
  const [dragging, setDragging] = createSignal(false);

  const startResize = (e: PointerEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startH = dock.height;
    setDragging(true);
    const move = (ev: PointerEvent) => {
      const max = window.innerHeight - 120;
      dockActions.setHeight(Math.min(max, startH + (startY - ev.clientY)));
    };
    const up = () => {
      setDragging(false);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  const onHandleKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowUp") dockActions.setHeight(dock.height + 24);
    else if (e.key === "ArrowDown") dockActions.setHeight(dock.height - 24);
    else return;
    e.preventDefault();
  };
  onCleanup(() => setDragging(false));

  return (
    <Show when={dock.tabs.length > 0}>
      <Show
        when={!isMobile()}
        fallback={
          <Sheet open={dock.open} onOpenChange={(o) => dockActions.setOpen(o)}>
            <SheetContent side="bottom" class="flex h-dvh flex-col gap-0 p-0" showCloseButton={false}>
              <SheetTitle class="sr-only">Dock</SheetTitle>
              <TabStrip />
              <DockPanes />
            </SheetContent>
          </Sheet>
        }
      >
        <section
          class={cn("relative flex shrink-0 flex-col border-t bg-card", !dock.open && "hidden")}
          style={{ height: maximized() ? "calc(100% - 4rem)" : `${dock.height}px`, "min-height": `${DOCK_MIN_HEIGHT}px` }}
          aria-label="Dock"
        >
          <div
            class={cn(
              "absolute inset-x-0 -top-1 z-20 h-2 cursor-row-resize focus-ring",
              dragging() && "bg-primary/30",
            )}
            role="separator"
            aria-orientation="horizontal"
            aria-label="Resize dock"
            aria-valuenow={dock.height}
            tabindex="0"
            onPointerDown={startResize}
            onKeyDown={onHandleKey}
            onDblClick={() => setMaximized((m) => !m)}
          />
          <TabStrip onMaximize={() => setMaximized((m) => !m)} maximized={maximized()} />
          <DockPanes />
        </section>
      </Show>
    </Show>
  );
}
