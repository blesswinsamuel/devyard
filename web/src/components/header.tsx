import { For, Show, createMemo } from "solid-js";
import {
  Globe,
  History,
  Play,
  Power,
  RefreshCw,
  RotateCcw,
  Skull,
  SquareTerminal,
} from "lucide-solid";
import type { ServiceState } from "~/lib/types";
import {
  actions as actionsMap,
  actionStates as actionStatesMap,
  fetchPorts,
  killService,
  ports,
  projects as projectsList,
  restartService,
  runAction,
  services as servicesMap,
  startProject,
  startService,
  stopProject,
  stopService,
} from "~/stores/data";
import { selectedAction, selectedProject, selectedService } from "~/stores/nav";
import {
  isPreviousLogs,
  tabKey,
  togglePreviousLogs,
} from "~/stores/logs";
import { openTerminalPanel, panelOpen, setShowPortsModal } from "~/stores/app";
import { healthTone, serviceMeta, statusLabel, statusTone } from "~/lib/status";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";

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
          class="text-muted-foreground hover:text-foreground"
          onClick={() => {
            fetchPorts(props.project);
            setShowPortsModal(true);
          }}
        >
          <Globe class="!size-3.5" />
          Ports
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
          class={panelOpen() ? "" : "text-muted-foreground hover:text-foreground"}
          onClick={() => openTerminalPanel()}
        >
          <SquareTerminal class="!size-3.5" />
          Terminal
        </TooltipTrigger>
        <TooltipContent>Toggle terminal panel (t)</TooltipContent>
      </Tooltip>
    </>
  );
}

function PrevRunButton(props: { project: string; kind: "service" | "action"; name: string }) {
  const key = () => tabKey(props.project, props.kind, props.name);
  return (
    <Tooltip>
      <TooltipTrigger
        as={Button}
        variant="ghost"
        size="sm"
        class={
          isPreviousLogs(key())
            ? "text-warning hover:text-warning"
            : "text-muted-foreground hover:text-foreground"
        }
        onClick={() => togglePreviousLogs(key())}
      >
        <History class="!size-3.5" />
        {isPreviousLogs(key()) ? "Previous run" : "Live"}
      </TooltipTrigger>
      <TooltipContent>Show previous run's logs (p)</TooltipContent>
    </Tooltip>
  );
}

function Breadcrumb(props: { parts: { label: string; strong?: boolean; mono?: boolean }[] }) {
  return (
    <div class="flex min-w-0 items-center gap-1.5 text-[13px]">
      <For each={props.parts}>
        {(part, i) => (
          <>
            {i() > 0 && <span class="text-muted-foreground/50">/</span>}
            <span
              classList={{
                truncate: true,
                "font-semibold": !!part.strong,
                "font-mono": !!part.mono,
                "text-muted-foreground": !part.strong && !part.mono,
              }}
            >
              {part.label}
            </span>
          </>
        )}
      </For>
    </div>
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
      <Breadcrumb
        parts={[
          { label: project() },
          { label: service(), strong: true },
        ]}
      />
      <Show when={state()}>
        {(s) => (
          <>
            <Badge variant={statusTone(s().status)}>{statusLabel(s().status, s().exit_code)}</Badge>
            <Show when={s().has_health}>
              <Badge variant={healthTone(s().has_health, s().health)}>health: {s().health}</Badge>
            </Show>
            <span data-tabular class="hidden truncate font-mono text-[11px] text-muted-foreground lg:inline">
              {serviceMeta(s())}
            </span>
          </>
        )}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <div class="mx-1 h-4 w-px bg-border" />
        <PrevRunButton project={project()} kind="service" name={service()} />
        <Show
          when={state()?.status !== "stopped" && state()?.status !== "exited"}
          fallback={
            <Button size="sm" onClick={() => startService(project(), service())}>
              <Play class="!size-3.5" />
              Start
            </Button>
          }
        >
          <Button size="sm" variant="secondary" onClick={() => restartService(project(), service())}>
            <RotateCcw class="!size-3.5" />
            Restart
          </Button>
          <Button size="sm" variant="outline" onClick={() => stopService(project(), service())}>
            <Power class="!size-3.5" />
            Stop
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
      <Breadcrumb parts={[{ label: project(), strong: true }]} />
      <Show when={info()}>
        {(p) => <Badge variant={statusTone(p().status)}>{p().status}</Badge>}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <div class="mx-1 h-4 w-px bg-border" />
        <Show
          when={!isStopped()}
          fallback={
            <Button size="sm" onClick={() => startProject(project())}>
              <Play class="!size-3.5" />
              Start project
            </Button>
          }
        >
          <Button size="sm" variant="outline" onClick={() => startProject(project())}>
            <RefreshCw class="!size-3.5" />
            Reload config
          </Button>
          <Button size="sm" variant="outline" onClick={() => stopProject(project())}>
            <Power class="!size-3.5" />
            Stop project
          </Button>
        </Show>
      </div>
    </>
  );
}

export function ActionHeader() {
  const project = () => selectedProject()!;
  const actionName = () => selectedAction()!;
  const act = createMemo(() => (actionsMap()[project()] ?? []).find((a) => a.name === actionName()));
  const state = createMemo(() =>
    (actionStatesMap()[project()] ?? []).find((a) => a.name === actionName())
  );
  const status = () => state()?.status ?? "idle";

  return (
    <>
      <Breadcrumb
        parts={[
          { label: project() },
          { label: "actions" },
          { label: actionName(), strong: true },
        ]}
      />
      <Show when={status() !== "idle"}>
        <Badge variant={statusTone(status())}>{statusLabel(status(), state()?.exit_code ?? 0)}</Badge>
      </Show>
      <Show when={act()}>
        {(a) => (
          <span class="hidden min-w-0 truncate font-mono text-[11px] text-muted-foreground md:inline">
            {a().command}
          </span>
        )}
      </Show>

      <div class="ml-auto flex shrink-0 items-center gap-1">
        <HeaderButtons project={project()} />
        <div class="mx-1 h-4 w-px bg-border" />
        <PrevRunButton project={project()} kind="action" name={actionName()} />
        <Button size="sm" onClick={() => runAction(project(), actionName())}>
          <Play class="!size-3.5" />
          Run action
        </Button>
      </div>
    </>
  );
}
