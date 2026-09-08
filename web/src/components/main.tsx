import { Show } from "solid-js";
import { Globe, Menu, PackageOpen, Play, Square, SquareTerminal } from "lucide-solid";
import { actions as actionsMap, fetchPorts, runAction, stopAction } from "~/stores/data";
import { activeView, selectedAction, selectedProject, selectedService } from "~/stores/nav";
import { openPortsModal, setSidebarOpen } from "~/stores/app";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "~/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { ActionHeader, ProjectHeader, ServiceHeader } from "~/components/header";
import { LogView } from "~/components/logs";
import { GitView } from "~/components/git/GitView";
import { BottomPanel } from "~/components/bottom_panel";
import { PortsModal } from "~/components/ports_modal";

function EmptyState() {
  const project = () => selectedProject();
  const actionList = () => (project() ? actionsMap()[project()!] ?? [] : []);

  return (
    <div class="flex h-full flex-col items-center justify-center p-6 text-muted-foreground">
      <Show
        when={project()}
        fallback={
          <Empty class="border-0">
            <EmptyHeader>
              <EmptyMedia>
                <PackageOpen class="size-9 stroke-1 text-muted-foreground" />
              </EmptyMedia>
              <EmptyTitle>No project selected</EmptyTitle>
              <EmptyDescription>
                Select a project to get started.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        }
      >
        <div class="w-full max-w-md text-center">
          <p class="mb-4 text-[13px] leading-relaxed">
            Select a service under{" "}
            <span class="font-semibold text-foreground">{project()}</span> to follow its logs.
          </p>
          <Show when={actionList().length > 0}>
            <Card class="shadow-sm">
              <CardHeader class="pb-2">
                <CardTitle class="text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground">
                  Project actions
                </CardTitle>
              </CardHeader>
              <CardContent class="grid gap-1.5 pt-0">
                {actionList().map((act) => (
                  <div class="flex items-center justify-between rounded-lg border border-border/70 bg-muted/30 px-2.5 py-2 transition-colors hover:bg-muted/60">
                    <div class="min-w-0 pr-2">
                      <div class="truncate text-[13px] font-medium text-foreground">{act.name}</div>
                      <div class="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
                        {act.command}
                      </div>
                    </div>
                    <Show
                      when={act.status === "running" || act.status === "starting"}
                      fallback={
                        <Button
                          size="xs"
                          variant="secondary"
                          class="shrink-0"
                          onClick={() => runAction(project()!, act.name)}
                        >
                          <Play class="!size-3 text-primary" />
                          Run
                        </Button>
                      }
                    >
                      <Button
                        size="xs"
                        variant="secondary"
                        class="shrink-0"
                        onClick={() => stopAction(project()!, act.name)}
                      >
                        <Square class="!size-3 text-destructive" />
                        Stop
                      </Button>
                    </Show>
                  </div>
                ))}
              </CardContent>
            </Card>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export function Main() {
  return (
    <main class="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-background">
      {/* Context header */}
      <Show when={activeView() !== "git"}>
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
                  local-compose
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  class="ml-auto mr-1 text-muted-foreground"
                  onClick={() => {
                    fetchPorts();
                    openPortsModal("all");
                  }}
                >
                  <Globe class="!size-3.5" />
                  <span class="hidden sm:inline">Open ports</span>
                </Button>
              </>
            }
          >
            <Show
              when={selectedService()}
              fallback={<Show when={selectedAction()} fallback={<ProjectHeader />}><ActionHeader /></Show>}
            >
              <ServiceHeader />
            </Show>
          </Show>
        </header>
      </Show>

      {/* Content */}
      <div class="relative min-h-0 flex-1 overflow-hidden">
        <Show when={activeView() !== "git"} fallback={<GitView />}>
          <Show
            when={selectedService() || selectedAction()}
            fallback={<EmptyState />}
          >
            <LogView />
          </Show>
        </Show>
      </div>

      <BottomPanel />
      <PortsModal />
    </main>
  );
}
