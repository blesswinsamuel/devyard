import { For, Show, createMemo, type JSX } from "solid-js";
import { GitBranch, PackageOpen, Plus, Rows2, SquareTerminal, Columns2 } from "lucide-solid";
import type { PaneNode, SplitNode, WorkspaceNode, WorkspaceTab } from "~/lib/types";
import {
  closeTab,
  focusedPaneId,
  focusPane,
  moveTabToPane,
  newTerminalTab,
  panesIn,
  reorderTab,
  root,
  selectTab,
  splitPane,
  updateSizes,
} from "~/stores/workspace";
import { selectedProject } from "~/stores/nav";
import { isMobile } from "~/lib/is-mobile";
import { cn } from "~/lib/utils";
import { TAB_DRAG_MIME, TabStrip, readTabDragData, type TabStripItem } from "~/components/ui/tabs";
import { Button } from "~/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { LogContent } from "~/components/workspace/log_content";
import { TerminalContent } from "~/components/workspace/terminal_content";
import { GitView } from "~/components/git/GitView";
import { OverviewContent } from "~/components/workspace/overview_content";

// --- tab descriptors ---------------------------------------------------------

function tabIcon(tab: WorkspaceTab): JSX.Element {
  switch (tab.kind) {
    case "log-service":
      return <span class="size-1.5 shrink-0 rounded-full bg-primary" />;
    case "log-task":
      return <span class="size-1.5 shrink-0 rounded-full bg-warning" />;
    case "git":
      return <GitBranch class="!size-3 shrink-0" />;
    case "terminal":
      return <SquareTerminal class="!size-3 shrink-0" />;
    case "overview":
      return <PackageOpen class="!size-3 shrink-0" />;
  }
}

function tabLabel(tab: WorkspaceTab): { prefix: string; title: string; tooltip: string } {
  const prefix = selectedProject() !== tab.project ? `${tab.project}/` : "";
  switch (tab.kind) {
    case "log-service":
      return { prefix, title: tab.service, tooltip: `${tab.project} · service` };
    case "log-task":
      return { prefix, title: tab.task, tooltip: `${tab.project} · task` };
    case "git":
      return { prefix, title: "git", tooltip: `${tab.project} · git` };
    case "terminal":
      return { prefix, title: "shell", tooltip: `${tab.project} · terminal` };
    case "overview":
      return { prefix: "", title: tab.project, tooltip: `${tab.project} · overview` };
  }
}

function toStripItem(tab: WorkspaceTab): TabStripItem {
  const { prefix, title, tooltip } = tabLabel(tab);
  return { id: tab.id, prefix, title, tooltip, icon: tabIcon(tab) };
}

// --- content switch ----------------------------------------------------------

function TabContent(props: { tab: WorkspaceTab; active: boolean }) {
  switch (props.tab.kind) {
    case "log-service":
    case "log-task":
      return <LogContent tab={props.tab} active={props.active} />;
    case "terminal":
      return <TerminalContent project={props.tab.project} termId={props.tab.termId} active={props.active} />;
    case "git":
      return (
        <div class="h-full w-full" classList={{ hidden: !props.active }}>
          <GitView project={props.tab.project} />
        </div>
      );
    case "overview":
      return (
        <div class="h-full w-full" classList={{ hidden: !props.active }}>
          <OverviewContent project={props.tab.project} />
        </div>
      );
  }
}

// --- split tree ---------------------------------------------------------------

function SplitView(props: { node: SplitNode }) {
  let containerRef!: HTMLDivElement;
  const children = () => props.node.children;

  const sizes = () => {
    const raw = props.node.sizes;
    if (raw && raw.length === children().length) return raw;
    return Array(children().length).fill(100 / children().length);
  };

  const startResize = (index: number, e: PointerEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const isVertical = props.node.direction === "vertical";
    const rect = containerRef.getBoundingClientRect();
    const totalPx = isVertical ? rect.width : rect.height;
    if (totalPx <= 0) return;
    const startPos = isVertical ? e.clientX : e.clientY;
    const initialSizes = [...sizes()];

    const onPointerMove = (moveEvent: PointerEvent) => {
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
      updateSizes(props.node.id, newSizes);
    };
    const onPointerUp = () => {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
    };
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    window.addEventListener("pointercancel", onPointerUp);
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
              <NodeView node={child} />
            </div>
            <Show when={idx() < children().length - 1}>
              <div
                class="group relative z-10 flex shrink-0 select-none touch-none items-center justify-center transition-colors"
                classList={{
                  "w-3 -mx-1.5 cursor-col-resize": isVertical(),
                  "h-3 -my-1.5 cursor-row-resize": !isVertical(),
                }}
                onPointerDown={(e) => startResize(idx(), e)}
              >
                <div
                  class="rounded-full bg-border-strong/70 transition-colors group-hover:bg-primary"
                  classList={{ "h-8 w-1": isVertical(), "w-8 h-1": !isVertical() }}
                />
              </div>
            </Show>
          </>
        )}
      </For>
    </div>
  );
}

function NodeView(props: { node: WorkspaceNode }): JSX.Element {
  // Reactive: the node type can change in place (pane → split when a lone
  // pane is split), so the branch must be a tracked Show, not an if.
  return (
    <Show
      when={props.node.type === "pane"}
      fallback={<SplitView node={props.node as SplitNode} />}
    >
      <WorkspacePane pane={props.node as PaneNode} />
    </Show>
  );
}

// --- panes ---------------------------------------------------------------------

function PaneEmptyState(props: { paneId: string }) {
  const project = () => selectedProject();
  return (
    <div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-muted-foreground">
      <Empty class="border-0">
        <EmptyHeader>
          <EmptyMedia>
            <PackageOpen class="size-8 stroke-1 text-muted-foreground" />
          </EmptyMedia>
          <EmptyTitle>Empty pane</EmptyTitle>
          <EmptyDescription class="max-w-xs">
            Select a service in the sidebar, or open a terminal here.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
      <Button size="sm" variant="outline" onClick={() => newTerminalTab(project() ?? "")} disabled={!project()}>
        <SquareTerminal class="!size-3.5" />
        New terminal
      </Button>
    </div>
  );
}

function WorkspacePane(props: { pane: PaneNode }) {
  const paneId = () => props.pane.id;
  const tabs = () => props.pane.tabs ?? [];
  const items = createMemo(() => tabs().map(toStripItem));

  // Splitting seeds the sibling pane with a fresh terminal when the active tab
  // is a terminal (mirrors the old shell pane behavior); otherwise it is empty.
  const splitSeed = (): WorkspaceTab | undefined => {
    const active = props.pane.tabs?.find((t) => t.id === props.pane.activeTabId);
    return active?.kind === "terminal" ? newTerminalTab(active.project) : undefined;
  };
  const splitRight = () => splitPane(paneId(), "vertical", splitSeed());
  const splitDown = () => splitPane(paneId(), "horizontal", splitSeed());

  const activeProject = () =>
    props.pane.tabs?.find((t) => t.id === props.pane.activeTabId)?.project ?? selectedProject();

  return (
    <div
      class="flex h-full w-full min-w-0 flex-col overflow-hidden rounded-lg border bg-card"
      onDragOver={(e) => {
        if (e.dataTransfer?.types.includes(TAB_DRAG_MIME)) e.preventDefault();
      }}
      onDrop={(e) => {
        const payload = readTabDragData(e);
        if (!payload) return;
        e.preventDefault();
        e.stopPropagation();
        moveTabToPane(payload.tabId, paneId());
      }}
      onMouseDown={() => focusPane(paneId())}
    >
      <div class="flex h-9 shrink-0 items-center gap-0.5 border-b bg-muted/30 px-1.5">
        <TabStrip
          class="flex-1"
          items={items()}
          activeId={props.pane.activeTabId}
          onSelect={(id) => selectTab(paneId(), id)}
          onClose={(id) => closeTab(id)}
          onReorder={(from, to) => reorderTab(paneId(), from, to)}
          onMoveTo={(payload, index) => moveTabToPane(payload.tabId, paneId(), index)}
          trailing={
            <div class="flex shrink-0 items-center gap-0.5 pl-1">
              <Button
                variant="ghost"
                size="icon-sm"
                class="text-muted-foreground"
                title="New terminal tab"
                onClick={() => newTerminalTab(props.pane.tabs?.find((t) => t.id === props.pane.activeTabId)?.project ?? selectedProject() ?? "")}
              >
                <Plus class="!size-3.5" />
              </Button>
              <Button variant="ghost" size="icon-sm" class="text-muted-foreground" title="Split right" onClick={splitRight}>
                <Columns2 class="!size-3" />
              </Button>
              <Button variant="ghost" size="icon-sm" class="text-muted-foreground" title="Split down" onClick={splitDown}>
                <Rows2 class="!size-3" />
              </Button>
            </div>
          }
        />
      </div>
      <div class="relative min-h-0 flex-1">
        <Show when={(props.pane.tabs?.length ?? 0) > 0} fallback={<PaneEmptyState paneId={paneId()} />}>
          <For each={tabs()}>
            {(tab) => <TabContent tab={tab} active={tab.id === props.pane.activeTabId} />}
          </For>
        </Show>
      </div>
    </div>
  );
}

// --- workspace root -------------------------------------------------------------

function WorkspaceEmptyState() {
  return (
    <div class="flex h-full flex-col items-center justify-center p-6 text-muted-foreground">
      <Empty class="border-0">
        <EmptyHeader>
          <EmptyMedia>
            <PackageOpen class="size-9 stroke-1 text-muted-foreground" />
          </EmptyMedia>
          <EmptyTitle>No tabs open</EmptyTitle>
          <EmptyDescription class="max-w-xs">
            Select a project or service in the sidebar to get started. Tabs stay
            open across projects — split panes to see several at once.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </div>
  );
}

/**
 * Desktop: the pane tree with splits. Mobile: splits are flattened into one
 * long strip of every open tab; only the focused tab renders.
 */
export function WorkspaceView() {
  const mobile = isMobile();
  const flat = createMemo(() => panesIn(root()).flatMap((p) => p.tabs));
  const activeId = createMemo(() => {
    const panes = panesIn(root());
    const pane = panes.find((p) => p.id === focusedPaneId()) ?? panes[0];
    return pane?.activeTabId ?? pane?.tabs[0]?.id ?? null;
  });

  return (
    <div class="h-full w-full p-1">
      <Show
        when={flat().length > 0}
        fallback={<WorkspaceEmptyState />}
      >
        <Show when={!mobile} fallback={
          <div class="flex h-full w-full flex-col overflow-hidden rounded-lg border bg-card">
            <div class="flex h-9 shrink-0 items-center border-b bg-muted/30 px-1.5">
              <TabStrip
                class="flex-1"
                items={flat().map(toStripItem)}
                activeId={activeId()}
                onSelect={(id) => {
                  const pane = panesIn(root()).find((p) => p.tabs.some((t) => t.id === id));
                  if (pane) selectTab(pane.id, id);
                }}
                onClose={(id) => closeTab(id)}
                trailing={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    class="shrink-0 text-muted-foreground"
                    title="New terminal tab"
                    onClick={() => newTerminalTab(selectedProject() ?? "")}
                  >
                    <Plus class="!size-3.5" />
                  </Button>
                }
              />
            </div>
            <div class="relative min-h-0 flex-1">
              <For each={flat()}>
                {(tab) => <TabContent tab={tab} active={tab.id === activeId()} />}
              </For>
            </div>
          </div>
        }>
          <NodeView node={root()} />
        </Show>
      </Show>
    </div>
  );
}
