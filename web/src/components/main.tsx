import { Show, createEffect, createMemo } from "solid-js";
import { GitBranch, Globe, Menu, SquareTerminal } from "lucide-solid";
import { projects as projectsList, services as servicesMap, tasks as tasksMap } from "~/stores/data";
import { selectedProject, openGitTab } from "~/stores/nav";
import { openPortsDialog, setSidebarOpen } from "~/stores/app";
import {
  focusedPaneActiveTab,
  panesIn,
  pruneWorkspace,
  root,
} from "~/stores/workspace";
import { Button } from "~/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { ProjectHeader, ServiceHeader, TaskHeader } from "~/components/header";
import { WorkspaceView } from "~/components/workspace/workspace";
import { PortsDialog } from "~/components/ports_panel";

function NoProjectState() {
  return (
    <div class="flex h-full flex-col items-center justify-center p-6 text-muted-foreground">
      <Empty class="border-0">
        <EmptyHeader>
          <EmptyMedia>
            <GitBranch class="size-9 stroke-1 text-muted-foreground" />
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

/** Git button: focuses the project's git tab, opening it in the focused pane. */
function GitButton() {
  return (
    <Button
      variant="ghost"
      size="sm"
      class="text-muted-foreground"
      title="Open git view (g)"
      onClick={() => {
        const project = selectedProject();
        if (project) openGitTab(project);
      }}
    >
      <GitBranch class="!size-3.5" />
      <span class="hidden sm:inline">Git</span>
    </Button>
  );
}

export function Main() {
  // Prune tabs whose targets vanished (project removed / service deleted).
  createEffect(() => {
    const projs = projectsList();
    const svcs = servicesMap();
    const tasks = tasksMap();

    pruneWorkspace((tab) => {
      if (projs.length > 0 && !projs.some((p) => p.name === tab.project)) {
        return false;
      }
      if (tab.kind === "log-service") {
        const list = svcs[tab.project];
        if (list !== undefined && !list.some((s) => s.name === tab.service)) {
          return false;
        }
      }
      if (tab.kind === "log-task") {
        const list = tasks[tab.project];
        if (list !== undefined && !list.some((a) => a.name === tab.task)) {
          return false;
        }
      }
      return true;
    });
  });

  const tabCount = createMemo(() => panesIn(root()).reduce((n, p) => n + p.tabs.length, 0));
  const activeTab = focusedPaneActiveTab;

  return (
    <main class="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-background">
      {/* Context header */}
      <header class="flex h-11 shrink-0 items-center gap-2 overflow-x-auto border-b bg-card/50 px-3 backdrop-blur">
        <Button
          variant="ghost"
          size="icon-sm"
          class="shrink-0 text-muted-foreground md:hidden"
          onClick={() => setSidebarOpen(true)}
          aria-label="Open menu"
        >
          <Menu class="!size-4" />
        </Button>
        <Show
          when={selectedProject()}
          fallback={
            <>
              <span class="flex items-center gap-2 text-[13px] font-semibold tracking-tight">
                <SquareTerminal class="size-4 text-primary" />
                devyard
              </span>
              <Button
                variant="ghost"
                size="sm"
                class="ml-auto mr-1 text-muted-foreground"
                onClick={() => openPortsDialog()}
              >
                <Globe class="!size-3.5" />
                <span class="hidden sm:inline">Ports</span>
              </Button>
            </>
          }
        >
          <Show
            when={activeTab()?.kind === "log-service"}
            fallback={
              <Show
                when={activeTab()?.kind === "log-task"}
                fallback={
                  <>
                    <ProjectHeader />
                    <GitButton />
                  </>
                }
              >
                <TaskHeader tab={activeTab()!} />
              </Show>
            }
          >
            <ServiceHeader tab={activeTab()!} />
          </Show>
        </Show>
      </header>

      {/* Unified workspace: pane tree of tab strips */}
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <Show when={tabCount() > 0} fallback={<NoProjectState />}>
          <WorkspaceView />
        </Show>
      </div>

      <PortsDialog />
    </main>
  );
}
