import { For, Show, createMemo } from "solid-js";
import { Plus } from "lucide-solid";
import type { PaneNode, SplitPaneNode, TerminalPaneNode } from "~/lib/types";
import {
  addShellTab,
  closeShellPane,
  closeShellTab,
  selectShellTab,
  selectedShellState,
  splitShellPane,
  updateSplitSizes,
} from "~/stores/shells";
import { selectedProject } from "~/stores/nav";
import { ShellPane } from "~/components/shell/shell_pane";
import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";

function SplitNodeContainer(props: { node: SplitPaneNode; project: string; active: boolean }) {
  let containerRef!: HTMLDivElement;
  const children = () => props.node.children;

  const sizes = () => {
    const raw = props.node.sizes;
    if (raw && raw.length === children().length) return raw;
    return Array(children().length).fill(100 / children().length);
  };

  const startResize = (index: number, e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const isVertical = props.node.direction === "vertical";
    const rect = containerRef.getBoundingClientRect();
    const totalPx = isVertical ? rect.width : rect.height;
    const startPos = isVertical ? e.clientX : e.clientY;
    const initialSizes = [...sizes()];

    const onMouseMove = (moveEvent: MouseEvent) => {
      const currentPos = isVertical ? moveEvent.clientX : moveEvent.clientY;
      const deltaPct = ((currentPos - startPos) / totalPx) * 100;
      const newSizes = [...initialSizes];
      const minPct = 10;
      let nextLeft = initialSizes[index]! + deltaPct;
      let nextRight = initialSizes[index + 1]! - deltaPct;
      const pairTotal = initialSizes[index]! + initialSizes[index + 1]!;
      if (nextLeft < minPct) {
        nextLeft = minPct;
        nextRight = pairTotal - minPct;
      }
      if (nextRight < minPct) {
        nextRight = minPct;
        nextLeft = pairTotal - minPct;
      }
      newSizes[index] = nextLeft;
      newSizes[index + 1] = nextRight;
      updateSplitSizes(props.project, props.node.id, newSizes);
    };
    const onMouseUp = () => {
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    };
    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
  };

  const isVertical = () => props.node.direction === "vertical";

  return (
    <div
      ref={containerRef}
      class="flex h-full w-full min-h-0 min-w-0 flex-1"
      classList={{ "flex-row": isVertical(), "flex-col": !isVertical() }}
    >
      <For each={children()}>
        {(child, idx) => (
          <>
            <div
              class="min-h-0 min-w-0"
              style={{ [isVertical() ? "width" : "height"]: `${sizes()[idx()]}%` }}
            >
              <PaneTree node={child} project={props.project} active={props.active} />
            </div>
            <Show when={idx() < children().length - 1}>
              <div
                class="relative z-10 flex shrink-0 select-none items-center justify-center transition-colors hover:bg-primary/60"
                classList={{
                  "w-px cursor-col-resize hover:w-1": isVertical(),
                  "h-px cursor-row-resize hover:h-1": !isVertical(),
                }}
                onMouseDown={(e) => startResize(idx(), e)}
              >
                <div
                  class="rounded-full bg-border-strong/70 group-hover:bg-primary"
                  classList={{ "h-8 w-[3px]": isVertical(), "w-8 h-[3px]": !isVertical() }}
                />
              </div>
            </Show>
          </>
        )}
      </For>
    </div>
  );
}

function PaneTree(props: { node: PaneNode; project: string; active: boolean }) {
  return (
    <Show
      when={props.node.type === "split"}
      fallback={
        <div class="h-full w-full min-h-0 min-w-0">
          <ShellPane
            id={(props.node as TerminalPaneNode).id}
            project={props.project}
            active={props.active}
            onSplitRight={() => splitShellPane(props.project, (props.node as TerminalPaneNode).id, "vertical")}
            onSplitDown={() => splitShellPane(props.project, (props.node as TerminalPaneNode).id, "horizontal")}
            onClose={() => closeShellPane(props.project, (props.node as TerminalPaneNode).id)}
          />
        </div>
      }
    >
      <SplitNodeContainer node={props.node as SplitPaneNode} project={props.project} active={props.active} />
    </Show>
  );
}

export function ShellWorkspace() {
  const project = () => selectedProject();
  const state = createMemo(() => selectedShellState());

  return (
    <Show when={state()}>
      {(s) => (
        <div class="flex h-full w-full flex-col bg-background p-1">
          <div class="relative min-h-0 flex-1">
            <For each={s().tabs}>
              {(tab) => {
                const isActive = () => tab.id === s().activeTabId;
                return (
                  <div class="h-full w-full" classList={{ hidden: !isActive() }}>
                    <PaneTree node={tab.rootPane} project={selectedProject()!} active={isActive()} />
                  </div>
                );
              }}
            </For>
          </div>
        </div>
      )}
    </Show>
  );
}

export function ShellTabBar() {
  const project = () => selectedProject();
  const state = createMemo(() => selectedShellState());

  return (
    <Show when={state()}>
      {(s) => (
        <div class="flex min-w-0 items-center gap-1 overflow-x-auto">
          <For each={s().tabs}>
            {(tab) => {
              const isActive = () => tab.id === s().activeTabId;
              return (
                <button
                  type="button"
                  onClick={() => selectShellTab(selectedProject()!, tab.id)}
                  class={cn(
                    "group flex h-6 shrink-0 items-center gap-1 rounded-md px-2 text-xs transition-colors",
                    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                    isActive()
                      ? "bg-accent font-medium text-accent-foreground"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground"
                  )}
                >
                  {tab.title}
                  <span
                    role="presentation"
                    class="flex size-3.5 items-center justify-center rounded-full opacity-50 transition-colors hover:bg-muted hover:opacity-100"
                    onClick={(e) => {
                      e.stopPropagation();
                      closeShellTab(selectedProject()!, tab.id);
                    }}
                  >
                    ×
                  </span>
                </button>
              );
            }}
          </For>
          <Button
            variant="ghost"
            size="icon-sm"
            class="shrink-0 text-muted-foreground hover:text-foreground"
            onClick={() => addShellTab(selectedProject()!)}
            title="New terminal tab"
          >
            <Plus class="!size-3.5" />
          </Button>
        </div>
      )}
    </Show>
  );
}
