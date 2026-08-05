import { For, Show, createMemo } from "solid-js";
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
} from "~/store";
import { ShellPane } from "~/components/shell_pane";
import { Button } from "~/components/ui/button";

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
      <div
        class="flex h-full w-full min-h-0 min-w-0 flex-1 gap-1"
        classList={{
          "flex-row": (props.node as SplitPaneNode).direction === "vertical",
          "flex-col": (props.node as SplitPaneNode).direction === "horizontal",
        }}
      >
        <For each={(props.node as SplitPaneNode).children}>
          {(child) => (
            <PaneTree node={child} project={props.project} active={props.active} />
          )}
        </For>
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
        {/* Tab Bar */}
        <div class="flex h-9 shrink-0 items-center gap-1 border-b border-border bg-muted/20 px-2 overflow-x-auto">
          <For each={shellState()!.tabs}>
            {(tab) => {
              const isActive = () => tab.id === shellState()!.activeTabId;
              return (
                <div
                  class="group flex h-7 items-center gap-1.5 rounded-t border px-2.5 text-xs transition-colors cursor-pointer select-none"
                  classList={{
                    "border-border bg-background font-medium text-foreground": isActive(),
                    "border-transparent bg-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground": !isActive(),
                  }}
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
            class="size-7 rounded text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => addShellTab(project()!)}
            title="New Terminal Tab"
          >
            <Plus class="size-4" />
          </Button>
        </div>

        {/* Workspace Content: Render all tabs, toggle hidden for inactive ones */}
        <div class="relative min-h-0 flex-1 p-1">
          <For each={shellState()!.tabs}>
            {(tab) => {
              const isActive = () => tab.id === shellState()!.activeTabId;
              return (
                <div
                  class="h-full w-full"
                  classList={{ hidden: !isActive() }}
                >
                  <PaneTree node={tab.rootPane} project={project()!} active={isActive()} />
                </div>
              );
            }}
          </For>
        </div>
      </div>
    </Show>
  );
}
