import { For, Show, createMemo } from "solid-js";
import { ChevronRight, Boxes, MoreVertical, Play, Power, RotateCcw, Skull, Sun, Moon } from "lucide-solid";
import {
  projects,
  services,
  isProjectExpanded,
  selectedProject,
  selectedService,
  keyboardCursor,
  sameNavItem,
  navItemKey,
  setProjectExpanded,
  selectProject,
  selectService,
  startProject,
  stopProject,
  restartService,
  stopService,
  killService,
  theme,
  setTheme,
  wsStatus,
  toggleHelp,
} from "~/store";
import { statusDot, statusTone, healthDot, serviceMeta } from "~/lib/status";
import { cn } from "~/lib/utils";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Switch } from "~/components/ui/switch";
import {
  CollapsibleRoot,
  CollapsibleTrigger,
  CollapsibleContent,
} from "~/components/ui/collapsible";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "~/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "~/components/ui/dropdown-menu";

function WsDot() {
  const color = () =>
    wsStatus() === "open"
      ? "bg-success"
      : wsStatus() === "connecting"
        ? "bg-warning animate-pulse"
        : "bg-destructive";
  return <span class={cn("inline-block size-2 shrink-0", color())} />;
}

function ServiceRow(props: { project: string; name: string }) {
  const service = createMemo(() =>
    (services()[props.project] ?? []).find((s) => s.name === props.name)
  );
  const selected = () =>
    selectedService() === props.name && selectedProject() === props.project;
  const cursor = () =>
    sameNavItem(keyboardCursor(), {
      kind: "service",
      project: props.project,
      service: props.name,
    });

  return (
    <Show when={service()}>
      {(s) => (
        <div
          class="group/svc relative flex items-stretch"
          data-nav-key={navItemKey({
            kind: "service",
            project: props.project,
            service: props.name,
          })}
          data-kbd-cursor={cursor() ? "" : undefined}
        >
          <button
            type="button"
            onClick={() => selectService(props.project, props.name)}
            class={cn(
              "flex h-7 min-w-0 flex-1 items-center gap-2 px-3 pl-7 pr-9 text-left text-sm transition-colors",
              "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
              selected() && "bg-muted text-foreground",
              cursor() && !selected() && "bg-muted/40 ring-1 ring-inset ring-ring/60",
              cursor() && selected() && "ring-1 ring-inset ring-ring"
            )}
          >
            <span class={cn("inline-block size-1.5 shrink-0", statusDot(s().status))} />
            <span class="truncate font-medium">{props.name}</span>
            <Show when={s().status === "exited" && s().exit_code !== 0}>
              <span class="shrink-0 text-xs text-destructive">({s().exit_code})</span>
            </Show>
            <Show when={s().restarts > 0}>
              <span class="shrink-0 text-xs text-muted-foreground" title={`${s().restarts} restart(s)`}>
                ↺{s().restarts}
              </span>
            </Show>
            <div class="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
              <Show when={s().has_health}>
                <Tooltip>
                  <TooltipTrigger class="flex shrink-0 items-center">
                    <span class={cn("inline-block size-2 rounded-full shrink-0", healthDot(s().has_health, s().health))} />
                  </TooltipTrigger>
                  <TooltipContent>health: {s().health}</TooltipContent>
                </Tooltip>
              </Show>
              <Show when={s().pid > 0}>
                <span class="font-mono text-[11px] opacity-80">{s().pid}</span>
              </Show>
            </div>
          </button>

          <div
            class={cn(
              "absolute right-1 top-1/2 flex -translate-y-1/2 items-center transition-opacity",
              selected()
                ? "opacity-100"
                : "opacity-0 pointer-events-none group-hover/svc:opacity-100 group-hover/svc:pointer-events-auto"
            )}
          >
            <DropdownMenu>
              <DropdownMenuTrigger
                as={Button}
                variant="ghost"
                size="icon-sm"
                class="border border-transparent bg-card/80 hover:border-border"
              >
                <MoreVertical />
                <span class="sr-only">Actions</span>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <Show
                  when={s().status === "stopped" || s().status === "exited"}
                  fallback={
                    <>
                      <DropdownMenuItem onSelect={() => restartService(props.project, props.name)}>
                        <RotateCcw />
                        Restart
                        <span class="ml-auto text-xs text-muted-foreground">r</span>
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => stopService(props.project, props.name)}>
                        <Power />
                        Stop
                        <span class="ml-auto text-xs text-muted-foreground">s</span>
                      </DropdownMenuItem>
                    </>
                  }
                >
                  <DropdownMenuItem onSelect={() => restartService(props.project, props.name)}>
                    <Play />
                    Start
                    <span class="ml-auto text-xs text-muted-foreground">r</span>
                  </DropdownMenuItem>
                </Show>
                <DropdownMenuItem
                  class="text-destructive focus:text-destructive"
                  onSelect={() => killService(props.project, props.name)}
                >
                  <Skull />
                  Kill
                  <span class="ml-auto text-xs text-muted-foreground">k</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      )}
    </Show>
  );
}

function ProjectItem(props: { name: string }) {
  const project = createMemo(() => projects().find((p) => p.name === props.name));
  /** undefined = not fetched yet; [] = fetched empty. */
  const serviceList = createMemo(() => services()[props.name]);
  const serviceNames = createMemo(() => (serviceList() ?? []).map((s) => s.name));
  const isOpen = () => isProjectExpanded(props.name);
  const active = () => selectedProject() === props.name;
  const selected = () => active() && selectedService() === null;
  const cursor = () =>
    sameNavItem(keyboardCursor(), { kind: "project", project: props.name });

  const projectHealth = createMemo(() => {
    const svcs = serviceList() ?? [];
    const withHealth = svcs.filter((s) => s.has_health);
    if (withHealth.length === 0) return null;
    if (withHealth.some((s) => s.health === "unhealthy")) {
      return { status: "unhealthy", label: "health: unhealthy" };
    }
    if (withHealth.some((s) => s.health === "starting" || s.health === "starting_healthy")) {
      return { status: "starting", label: "health: starting" };
    }
    if (withHealth.every((s) => s.health === "healthy")) {
      return { status: "healthy", label: "health: healthy" };
    }
    return null;
  });

  return (
    <Show when={project()}>
      {(p) => (
        <CollapsibleRoot
          open={isOpen()}
          onOpenChange={(open) => setProjectExpanded(props.name, open)}
        >
          <div
            class={cn(
              "group/project flex items-center border-l-2 transition-colors",
              active() ? "border-primary" : "border-transparent",
              selected() ? "bg-muted" : "hover:bg-muted/60",
              cursor() && !selected() && "bg-muted/40 ring-1 ring-inset ring-ring/60",
              cursor() && selected() && "ring-1 ring-inset ring-ring"
            )}
            data-nav-key={navItemKey({ kind: "project", project: props.name })}
            data-kbd-cursor={cursor() ? "" : undefined}
          >
            <CollapsibleTrigger class="flex h-8 w-7 shrink-0 items-center justify-center text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring data-[expanded]:text-foreground [&[data-expanded]_svg]:rotate-90">
              <ChevronRight class="size-4 transition-transform" />
              <span class="sr-only">Toggle</span>
            </CollapsibleTrigger>
            <button
              type="button"
              onClick={() => selectProject(props.name)}
              class="flex min-w-0 flex-1 items-center gap-2 py-1.5 pr-1 text-left text-sm font-medium focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <Boxes class="size-3.5 shrink-0 text-muted-foreground" />
              <span class="truncate">{props.name}</span>
              <div class="ml-auto flex items-center gap-1.5">
                <Show when={projectHealth()}>
                  {(ph) => (
                    <Tooltip>
                      <TooltipTrigger class="flex shrink-0 items-center">
                        <span class={cn("inline-block size-2 rounded-full shrink-0", healthDot(true, ph().status))} />
                      </TooltipTrigger>
                      <TooltipContent>{ph().label}</TooltipContent>
                    </Tooltip>
                  )}
                </Show>
                <Badge variant={statusTone(p().status)} class="capitalize">
                  {p().status}
                </Badge>
              </div>
            </button>
            <div class="flex shrink-0 items-center pr-1.5">
              <Show
                when={p().status !== "stopped"}
                fallback={
                  <Tooltip>
                    <TooltipTrigger
                      as={Button}
                      variant="ghost"
                      size="icon-sm"
                      class={cn(
                        "transition-opacity",
                        "opacity-0 pointer-events-none",
                        "group-hover/project:opacity-100 group-hover/project:pointer-events-auto",
                        "focus-visible:opacity-100 focus-visible:pointer-events-auto"
                      )}
                      onClick={(e) => {
                        e.stopPropagation();
                        startProject(props.name);
                      }}
                    >
                      <Play />
                      <span class="sr-only">Start project</span>
                    </TooltipTrigger>
                    <TooltipContent>Start project (d)</TooltipContent>
                  </Tooltip>
                }
              >
                <Tooltip>
                  <TooltipTrigger
                    as={Button}
                    variant="ghost"
                    size="icon-sm"
                    class={cn(
                      "transition-opacity",
                      "opacity-0 pointer-events-none",
                      "group-hover/project:opacity-100 group-hover/project:pointer-events-auto",
                      "focus-visible:opacity-100 focus-visible:pointer-events-auto"
                    )}
                    onClick={(e) => {
                      e.stopPropagation();
                      stopProject(props.name);
                    }}
                  >
                    <Power />
                    <span class="sr-only">Stop project</span>
                  </TooltipTrigger>
                  <TooltipContent>Stop project (d)</TooltipContent>
                </Tooltip>
              </Show>
            </div>
          </div>
          <CollapsibleContent>
            <div class="border-l border-border py-1">
              <Show
                when={serviceList() !== undefined}
                fallback={
                  <p class="px-7 py-1 text-xs text-muted-foreground">loading…</p>
                }
              >
                <Show
                  when={serviceNames().length > 0}
                  fallback={
                    <p class="px-7 py-1 text-xs text-muted-foreground">no services</p>
                  }
                >
                  <For each={serviceNames()}>
                    {(name) => <ServiceRow project={props.name} name={name} />}
                  </For>
                </Show>
              </Show>
            </div>
          </CollapsibleContent>
        </CollapsibleRoot>
      )}
    </Show>
  );
}

function ThemeToggle() {
  return (
    <div class="flex items-center gap-2">
      <Show when={theme() === "dark"} fallback={<Sun class="size-4 text-muted-foreground" />}>
        <Moon class="size-4 text-muted-foreground" />
      </Show>
      <Switch checked={theme() === "dark"} onChange={(v) => setTheme(v ? "dark" : "light")} />
    </div>
  );
}

export function Sidebar() {
  const projectNames = createMemo(() => projects().map((p) => p.name));

  return (
    <aside class="flex h-full w-72 shrink-0 flex-col border-r border-border bg-card">
      <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
        <Boxes class="size-5 text-primary" />
        <span class="font-semibold tracking-wide">local-compose</span>
        <span class="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
          <WsDot />
          {wsStatus()}
        </span>
      </header>

      <nav
        class="flex-1 overflow-y-auto py-2 outline-none"
        data-sidebar-nav
        tabindex="-1"
      >
        <p class="px-4 pb-1 text-xs uppercase tracking-wider text-muted-foreground">
          Projects
        </p>
        <For each={projectNames()}>
          {(name) => <ProjectItem name={name} />}
        </For>
        <Show when={projectNames().length === 0}>
          <p class="px-4 py-4 text-xs leading-relaxed text-muted-foreground">
            No projects running. Start one with <code class="text-foreground">local-compose up</code>.
          </p>
        </Show>
      </nav>

      <footer class="flex h-11 shrink-0 items-center justify-between gap-2 border-t border-border px-3">
        <button
          type="button"
          class="text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          onClick={() => toggleHelp()}
          title="Keyboard shortcuts"
        >
          <kbd class="border border-border bg-muted px-1 py-px font-mono">?</kbd>
          <span class="ml-1.5">shortcuts</span>
        </button>
        <ThemeToggle />
      </footer>
    </aside>
  );
}
