import { Show, createMemo } from "solid-js";
import { Play, Square } from "lucide-solid";
import { runTask, stopTask, tasks as tasksMap } from "~/stores/data";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "~/components/ui/card";
import { PortsPanel } from "~/components/ports_panel";

/** Project landing page: pointers, ports & URLs, and project tasks. */
export function OverviewContent(props: { project: string }) {
  const taskList = createMemo(() => tasksMap()[props.project] ?? []);

  return (
    <div class="h-full overflow-y-auto">
      <div class="mx-auto flex w-full max-w-2xl flex-col gap-4 p-6">
        <p class="text-[13px] leading-relaxed text-muted-foreground">
          Select a service under{" "}
          <span class="font-semibold text-foreground">{props.project}</span> to follow its logs.
        </p>

        <Card class="shadow-sm">
          <CardHeader class="pb-2">
            <CardTitle class="text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground">
              Ports & URLs
            </CardTitle>
          </CardHeader>
          <CardContent class="pt-0">
            <PortsPanel project={props.project} />
          </CardContent>
        </Card>

        <Show when={taskList().length > 0}>
          <Card class="shadow-sm">
            <CardHeader class="pb-2">
              <CardTitle class="text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground">
                Project tasks
              </CardTitle>
            </CardHeader>
            <CardContent class="grid gap-1.5 pt-0">
              {taskList().map((act) => (
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
                        onClick={() => runTask(props.project, act.name)}
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
                      onClick={() => stopTask(props.project, act.name)}
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
    </div>
  );
}
