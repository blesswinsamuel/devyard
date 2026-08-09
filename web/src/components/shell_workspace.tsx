import { For, Show, createEffect, createMemo, createSignal } from "solid-js";
import { Plus, X } from "lucide-solid";
import type { PaneNode, SplitPaneNode, TerminalPaneNode } from "~/types";
import {
  selectedProject,
  getProjectShellState,
  addShellTab,
  selectShellTab,
  closeShellTab,
  splitShellPane,
  closeShellPane,
  updateSplitSizes,
  reorderShellTabs,
  movePaneToNewTab,
} from "~/store";
import { ShellPane } from "~/components/shell_pane";
import { Button } from "~/components/ui/button";

function SplitNodeContainer(props: { node: SplitPaneNode; project: string; active: boolean }) {
  let containerRef!: HTMLDivElement;

  const children = () => props.node.children;
  const count = () => children().length;

  const sizes = () => {
    const raw = props.node.sizes;
    if (raw && raw.length === count()) return raw;
    const equal = 100 / count();
    return Array(count()).fill(equal);
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
      const deltaPx = currentPos - startPos;
      const deltaPct = (deltaPx / totalPx) * 100;

      const newSizes = [...initialSizes];
      const minPct = 10;
      let nextLeft = initialSizes[index] + deltaPct;
      let nextRight = initialSizes[index + 1] - deltaPct;

      if (nextLeft < minPct) {
        nextLeft = minPct;
        nextRight = initialSizes[index] + initialSizes[index + 1] - minPct;
      }
      if (nextRight < minPct) {
        nextRight = minPct;
        nextLeft = initialSizes[index] + initialSizes[index + 1] - minPct;
      }

      newSizes[index] = nextLeft;
      newSizes[index + 1] = nextRight;
      updateSplitSizes(props.project, props.node.id, newSizes);
      window.dispatchEvent(new Event("resize"));
    };

    const onMouseUp = () => {
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
      window.dispatchEvent(new Event("resize"));
    };

    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
  };

  const isVertical = () => props.node.direction === "vertical";

  return (
    <div
      ref={containerRef}
      class="flex h-full w-full min-h-0 min-w-0 flex-1"
      classList={{
        "flex-row": isVertical(),
        "flex-col": !isVertical(),
      }}
    >
      <For each={children()}>
        {(child, idx) => {
          const sz = () => sizes()[idx()];
          return (
            <>
              <div
                class="min-h-0 min-w-0 flex-shrink-0"
                style={{
                  [isVertical() ? "width" : "height"]: `${sz()}%`,
                }}
              >
                <PaneTree node={child} project={props.project} active={props.active} />
              </div>
              <Show when={idx() < count() - 1}>
                <div
                  class="group relative z-10 flex shrink-0 items-center justify-center bg-border/40 hover:bg-primary transition-colors select-none"
                  classList={{
                    "w-1.5 cursor-col-resize hover:w-2": isVertical(),
                    "h-1.5 cursor-row-resize hover:h-2": !isVertical(),
                  }}
                  onMouseDown={(e) => startResize(idx(), e)}
                >
                  <div
                    class="rounded-full bg-border group-hover:bg-primary transition-colors"
                    classList={{
                      "h-6 w-0.5": isVertical(),
                      "w-6 h-0.5": !isVertical(),
                    }}
                  />
                </div>
              </Show>
            </>
          );
        }}
      </For>
    </div>
  );
}

function PaneTree(props: { node: PaneNode; project: string; active: boolean }) {
  return (
    <Show
      when={props.node.type === "split"}
      fallback={
        <div class="h-full w-full min-h-0 min-w-0 flex-1">
          <ShellPane
            id={(props.node as TerminalPaneNode).id}
            project={props.project}
            active={props.active}
            onSplitRight={() =>
              splitShellPane(
                props.project,
                (props.node as TerminalPaneNode).id,
                "vertical"
              )
            }
            onSplitDown={() =>
              splitShellPane(
                props.project,
                (props.node as TerminalPaneNode).id,
                "horizontal"
              )
            }
            onClose={() =>
              closeShellPane(
                props.project,
                (props.node as TerminalPaneNode).id
              )
            }
          />
        </div>
      }
    >
      <SplitNodeContainer
        node={props.node as SplitPaneNode}
        project={props.project}
        active={props.active}
      />
    </Show>
  );
}

export function ShellTabBar() {
  const project = () => selectedProject();
  const shellState = createMemo(() => {
    const p = project();
    return p ? getProjectShellState(p) : null;
  });

  const [draggedTabIndex, setDraggedTabIndex] = createSignal<number | null>(null);
  const [dragOverTabIndex, setDragOverTabIndex] = createSignal<number | null>(null);
  const [isTabBarDropTarget, setIsTabBarDropTarget] = createSignal(false);

  const handleTabDragStart = (idx: number, e: DragEvent) => {
    setDraggedTabIndex(idx);
    e.dataTransfer?.setData("application/x-local-compose-tab", idx.toString());
  };

  const handleTabDragOver = (idx: number, e: DragEvent) => {
    if (e.dataTransfer?.types.includes("application/x-local-compose-tab")) {
      e.preventDefault();
      setDragOverTabIndex(idx);
    }
  };

  const handleTabDrop = (targetIdx: number, e: DragEvent) => {
    const sourceIdx = draggedTabIndex();
    setDraggedTabIndex(null);
    setDragOverTabIndex(null);
    if (sourceIdx !== null && sourceIdx !== targetIdx && project()) {
      e.preventDefault();
      reorderShellTabs(project()!, sourceIdx, targetIdx);
    }
  };

  const handleTabBarDragOver = (e: DragEvent) => {
    if (e.dataTransfer?.types.includes("application/x-local-compose-pane")) {
      e.preventDefault();
      setIsTabBarDropTarget(true);
    }
  };

  const handleTabBarDrop = (e: DragEvent) => {
    setIsTabBarDropTarget(false);
    const paneId = e.dataTransfer?.getData("application/x-local-compose-pane");
    if (paneId && project()) {
      e.preventDefault();
      movePaneToNewTab(project()!, paneId);
    }
  };

  return (
    <Show when={project() && shellState()}>
      <div
        class="flex items-center gap-1 overflow-x-auto transition-colors"
        classList={{
          "bg-primary/10 rounded px-1": isTabBarDropTarget(),
        }}
        onDragOver={handleTabBarDragOver}
        onDragLeave={() => setIsTabBarDropTarget(false)}
        onDrop={handleTabBarDrop}
      >
        <For each={shellState()!.tabs}>
          {(tab, idx) => {
            const isActive = () => tab.id === shellState()!.activeTabId;
            const isOver = () => dragOverTabIndex() === idx();
            return (
              <div
                class="group flex h-7 items-center gap-1.5 rounded border px-2.5 text-xs transition-all cursor-pointer select-none shrink-0"
                classList={{
                  "border-border bg-background font-medium text-foreground shadow-sm": isActive(),
                  "border-transparent bg-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground": !isActive(),
                  "border-primary text-primary": isOver(),
                }}
                draggable="true"
                onDragStart={(e) => handleTabDragStart(idx(), e)}
                onDragOver={(e) => handleTabDragOver(idx(), e)}
                onDragLeave={() => setDragOverTabIndex(null)}
                onDrop={(e) => handleTabDrop(idx(), e)}
                onClick={() => selectShellTab(project()!, tab.id)}
              >
                <span>{tab.title}</span>
                <button
                  type="button"
                  class="flex size-4 items-center justify-center rounded-full p-0 text-muted-foreground opacity-60 hover:bg-muted hover:text-foreground hover:opacity-100 transition-opacity"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeShellTab(project()!, tab.id);
                  }}
                  title="Close Tab"
                >
                  <X class="size-3" />
                </button>
              </div>
            );
          }}
        </For>

        <Button
          variant="ghost"
          size="icon"
          class="size-7 rounded text-muted-foreground hover:bg-muted hover:text-foreground shrink-0"
          onClick={() => addShellTab(project()!)}
          title={isTabBarDropTarget() ? "Drop pane here to create tab" : "New Terminal Tab"}
        >
          <Plus class="size-4" />
        </Button>

        <Show when={isTabBarDropTarget()}>
          <span class="text-xs text-primary font-medium animate-pulse ml-2 whitespace-nowrap">
            Drop pane here to move to new tab
          </span>
        </Show>
      </div>
    </Show>
  );
}

export function ShellWorkspace() {
  const project = () => selectedProject();
  const shellState = createMemo(() => {
    const p = project();
    return p ? getProjectShellState(p) : null;
  });

  return (
    <Show when={project() && shellState()}>
      <div class="flex h-full w-full flex-col bg-background">
        {/* Workspace Content: Render all tabs, toggle hidden for inactive ones */}
        <div class="relative min-h-0 flex-1 p-1">
          <For each={shellState()!.tabs}>
            {(tab) => {
              const isActive = () => tab.id === shellState()!.activeTabId;
              const [visited, setVisited] = createSignal(isActive());
              createEffect(() => {
                if (isActive()) setVisited(true);
              });
              return (
                <Show when={visited()}>
                  <div
                    class="h-full w-full"
                    classList={{ hidden: !isActive() }}
                  >
                    <PaneTree node={tab.rootPane} project={project()!} active={isActive()} />
                  </div>
                </Show>
              );
            }}
          </For>
        </div>
      </div>
    </Show>
  );
}

