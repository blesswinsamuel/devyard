import { For, Show, createMemo } from "solid-js";
import {
  ExternalLink,
  Globe,
  History,
  Play,
  Power,
  RefreshCw,
  RotateCcw,
  Skull,
  Square,
  SquareTerminal,
} from "lucide-solid";
import type { ServiceState } from "~/lib/types";
import {
  fetchPorts,
  killService,
  ports,
  projects as projectsList,
  restartService,
  runTask,
  services as servicesMap,
  startProject,
  startService,
  stopProject,
  stopService,
  stopTask,
  tasks as tasksMap,
} from "~/stores/data";
import { selectedProject, selectedService, selectedTask } from "~/stores/nav";
import {
  isPreviousLogs,
  tabKey,
  togglePreviousLogs,
} from "~/stores/logs";
import { openPortsModal, openTerminalPanel, panelOpen } from "~/stores/app";
import { healthTone, serviceMeta, statusLabel, statusTone } from "~/lib/status";
import { Badge } from "~/components/ui/badge";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "~/components/ui/breadcrumb";
import { Button, buttonVariants } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import { cleanProxyUrl } from "~/lib/format";



/** Buttons shared by every header variant. */
function HeaderButtons(props: { project: string }) {
  const count = () => (ports()[props.project] ?? []).length;
  return (
    <>
      <Tooltip>
        <TooltipTrigger
          as={Button}
          variant="ghost"
          size="sm"
          class="text-muted-foreground"
          onClick={() => {
            fetchPorts(props.project);
            openPortsModal("project");
          }}
        >
          <Globe class="!size-3.5" />
          <span class="hidden md:inline">Ports</span>
          <Show when={count() > 0}>
            <span class="rounded-full bg-primary/12 px-1.5 font-mono text-[10px] tabular text-primary">
              {count()}
            </span>
          </Show>
        </TooltipTrigger>
        <TooltipContent>Open ports</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger
          as={Button}
          variant={panelOpen() ? "secondary" : "ghost"}
          size="sm"
          class={panelOpen() ? "" : "text-muted-foreground"}
          onClick={() => openTerminalPanel()}
        >
          <SquareTerminal class="!size-3.5" />
          <span class="hidden md:inline">Terminal</span>
        </TooltipTrigger>
        <TooltipContent>Toggle terminal panel (t)</TooltipContent>
      </Tooltip>
    </>
  );
}

function PrevRunButton(props: { project: string; kind: "service" | "task"; name: string }) {
  const key = () => tabKey(props.project, props.kind, props.name);
  return (
    <Tooltip>
      <TooltipTrigger
        as={Button}
        variant="ghost"
        size="sm"
        class={isPreviousLogs(key()) ? "text-warning" : "text-muted-foreground"}
        onClick={() => togglePreviousLogs(key())}
      >
        <History class="!size-3.5" />
        <span class="hidden md:inline">{isPreviousLogs(key()) ? "Previous run" : "Live"}</span>
      </TooltipTrigger>
      <TooltipContent>Show previous run's logs (p)</TooltipContent>
    </Tooltip>
  );
}

function HeaderBreadcrumb(props: { parts: { label: string; strong?: boolean; mono?: boolean }[] }) {
  return (
    <Breadcrumb class="min-w-0">
      <BreadcrumbList class="flex-nowrap gap-1.5 text-[13px]">
        <For each={props.parts}>
          {(part, i) => (
            <>
              {i() > 0 && <BreadcrumbSeparator class="text-[11px]">/</BreadcrumbSeparator>}
              <BreadcrumbItem class="min-w-0">
                <BreadcrumbPage
                  classList={{
                    truncate: true,
                    "font-semibold text-foreground": !!part.strong,
                    "font-mono": !!part.mono,
                  }}
                >
                  {part.label}
                </BreadcrumbPage>
              </BreadcrumbItem>
            </>
          )}
        </For>
      </BreadcrumbList>
    </Breadcrumb>
  );
}

export function ServiceHeader() {
  const project = () => selectedProject()!;
  const service = () => selectedService()!;
  const state = createMemo<ServiceState | undefined>(() =>
    (servicesMap()[project()] ?? []).find((s) => s.name === service())
  );

  return (
    <>
      <HeaderBreadcrumb
        parts={[
          { label: project() },
          { label: service(), strong: true },
        ]}
      />
      <Show when={state()}>
        {(s) => (
          <>
            <Badge variant={statusTone(s().status)}>{statusLabel(s().status, s().exitCode ?? (s() as any).exit_code ?? 0)}</Badge>
            <Show when={(s().hasHealth ?? (s() as any).has_health) && (s().status === "running" || s().status === "starting")}>
              <Badge variant={healthTone(s().hasHealth ?? (s() as any).has_health, s().health)} class="max-[420px]:hidden">
                health: {s().health}
              </Badge>
            </Show>
            <For each={s().proxyUrls}>
              {(url) => (
                <Tooltip>
                  <TooltipTrigger
                    as="a"
                    href={url}
                    target="_blank"
                    rel="noreferrer"
                    class={cn(
                      buttonVariants({ variant: "outline", size: "sm" }),
                      "h-6 gap-1 px-2 font-mono text-xs text-primary hover:text-primary transition-colors"
                    )}
                  >
                    <span>{cleanProxyUrl(url)}</span>
                    <ExternalLink class="!size-3 text-muted-foreground" />
                  </TooltipTrigger>
                  <TooltipContent>Open {url} in browser</TooltipContent>
                </Tooltip>
              )}
            </For>
            <span data-tabular class="hidden truncate font-mono text-[11px] text-muted-foreground lg:inline">
              {serviceMeta(s())}
            </span>
          </>
        )}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <Separator orientation="vertical" class="mx-1 h-4" />
        <PrevRunButton project={project()} kind="service" name={service()} />
        <Show
          when={state()?.status !== "stopped" && state()?.status !== "exited"}
          fallback={
            <Button size="sm" aria-label="Start service" onClick={() => startService(project(), service())}>
              <Play class="!size-3.5" />
              <span class="hidden md:inline">Start</span>
            </Button>
          }
        >
          <Button size="sm" variant="secondary" aria-label="Restart service" onClick={() => restartService(project(), service())}>
            <RotateCcw class="!size-3.5" />
            <span class="hidden md:inline">Restart</span>
          </Button>
          <Button size="sm" variant="outline" aria-label="Stop service" onClick={() => stopService(project(), service())}>
            <Power class="!size-3.5" />
            <span class="hidden md:inline">Stop</span>
          </Button>
        </Show>
        <Tooltip>
          <TooltipTrigger
            as={Button}
            variant="ghost"
            size="icon-sm"
            class="text-destructive/80 hover:bg-destructive/10 hover:text-destructive"
            onClick={() => killService(project(), service())}
          >
            <Skull class="!size-3.5" />
            <span class="sr-only">Kill service</span>
          </TooltipTrigger>
          <TooltipContent>Kill service — press k twice</TooltipContent>
        </Tooltip>
      </div>
    </>
  );
}

export function ProjectHeader() {
  const project = () => selectedProject()!;
  const info = createMemo(() => projectsList().find((p) => p.name === project()));
  const isStopped = () => info()?.status === "stopped";

  return (
    <>
      <HeaderBreadcrumb parts={[{ label: project(), strong: true }]} />
      <Show when={info()}>
        {(p) => <Badge variant={statusTone(p().status)}>{p().status}</Badge>}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <Separator orientation="vertical" class="mx-1 h-4" />
        <Show
          when={!isStopped()}
          fallback={
            <Button size="sm" aria-label="Start project" onClick={() => startProject(project())}>
              <Play class="!size-3.5" />
              <span class="hidden md:inline">Start project</span>
            </Button>
          }
        >
          <Button size="sm" variant="outline" aria-label="Reload config" onClick={() => startProject(project())}>
            <RefreshCw class="!size-3.5" />
            <span class="hidden md:inline">Reload config</span>
          </Button>
          <Button size="sm" variant="outline" aria-label="Stop project" onClick={() => stopProject(project())}>
            <Power class="!size-3.5" />
            <span class="hidden md:inline">Stop project</span>
          </Button>
        </Show>
      </div>
    </>
  );
}

export function TaskHeader() {
  const project = () => selectedProject()!;
  const taskName = () => selectedTask()!;
  const task = createMemo(() => (tasksMap()[project()] ?? []).find((t) => t.name === taskName()));
  const status = () => task()?.status ?? "idle";

  return (
    <>
      <HeaderBreadcrumb
        parts={[
          { label: project() },
          { label: "tasks" },
          { label: taskName(), strong: true },
        ]}
      />
      <Show when={status() !== "idle"}>
        <Badge variant={statusTone(status())}>{statusLabel(status(), task()?.exitCode ?? (task() as any)?.exit_code ?? 0)}</Badge>
      </Show>
      <Show when={task()}>
        {(t) => (
          <span class="hidden min-w-0 truncate font-mono text-[11px] text-muted-foreground md:inline">
            {t().command}
          </span>
        )}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <Separator orientation="vertical" class="mx-1 h-4" />
        <PrevRunButton project={project()} kind="task" name={taskName()} />
        <Show
          when={status() === "running" || status() === "starting"}
          fallback={
            <Button size="sm" aria-label="Run task" onClick={() => runTask(project(), taskName())}>
              <Play class="!size-3.5" />
              <span class="hidden md:inline">Run task</span>
            </Button>
          }
        >
          <Button size="sm" variant="outline" aria-label="Stop task" onClick={() => stopTask(project(), taskName())}>
            <Square class="!size-3.5 text-destructive" />
            <span class="hidden md:inline">Stop task</span>
          </Button>
          <Button size="sm" variant="destructive" aria-label="Kill task" onClick={() => killService(project(), taskName())}>
            <Skull class="!size-3.5" />
            <span class="hidden md:inline">Kill</span>
          </Button>
        </Show>
      </div>
    </>
  );
}
