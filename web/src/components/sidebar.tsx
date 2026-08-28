import { For, Show, createMemo } from "solid-js";
import {
  Boxes,
  ChevronRight,
  GitBranch,
  MoreVertical,
  Play,
  Plus,
  Power,
  RefreshCw,
  RotateCcw,
  Skull,
  X,
} from "lucide-solid";
import {
  isProjectExpanded,
  keyboardCursor,
  navItemKey,
  sameNavItem,
  selectAction,
  selectProject,
  selectService,
  selectedAction,
  selectedProject,
  selectedService,
  setProjectExpanded,
} from "~/stores/nav";
import {
  actions as actionsMap,
  fetchDaemonStatus,
  actionStates as actionStatesMap,
  daemonInfo,
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
import { setShowAddProject, setShowDaemonModal, theme, setTheme } from "~/stores/app";
import { toggleHelp, sidebarOpen, setSidebarOpen } from "~/stores/app";
import { openGitView } from "~/stores/nav";
import { wsStatus } from "~/lib/ws";
import { healthDot, statusDot, statusTone } from "~/lib/status";
import { cn } from "~/lib/utils";
import { Badge } from "~/components/ui/badge";
import { Kbd } from "~/components/ui/kbd";
import { Button } from "~/components/ui/button";
import { Switch } from "~/components/ui/switch";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "~/components/ui/collapsible";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";

function WsDot() {
  return (
    <span
      class={cn("inline-block size-2 shrink-0 rounded-full", {
        "bg-success shadow-[0_0_6px_var(--success)]": wsStatus() === "open",
        "bg-warning animate-pulse": wsStatus() === "connecting",
        "bg-destructive": wsStatus() === "closed",
      })}
    />
  );
}

function rowClasses(selected: boolean, cursor: boolean): string {
  return cn(
    "transition-colors",
    selected && "bg-accent text-accent-foreground",
    !selected && cursor && "bg-muted",
    !selected && "hover:bg-muted/70"
  );
}

function ServiceRow(props: { project: string; name: string }) {
  const service = createMemo(() =>
    (servicesMap()[props.project] ?? []).find((s) => s.name === props.name)
  );
  const selected = () =>
    selectedService() === props.name && selectedProject() === props.project;
  const cursor = () =>
    sameNavItem(keyboardCursor(), { kind: "service", project: props.project, service: props.name });
  const portBinding = createMemo(() =>
    (ports()[props.project] ?? []).find((p) => p.service === props.name)
  );

  return (
    <Show when={service()}>
      {(s) => (
        <div
          class="group/svc relative flex items-stretch"
          data-kbd-cursor={cursor() ? "" : undefined}
        >
          <button
            type="button"
            onClick={() => selectService(props.project, props.name)}
            class={cn(
              "flex h-[30px] min-w-0 flex-1 items-center gap-2 py-0 pl-8 pr-2.5 text-left text-[13px]",
              "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring",
              rowClasses(selected(), cursor())
            )}
          >
            <span class={cn("size-1.5 shrink-0 rounded-full transition-shadow", statusDot(s().status))} />
            <span class="truncate">{props.name}</span>
            <Show when={s().status === "exited" && s().exit_code !== 0}>
              <span class="shrink-0 font-mono text-[11px] tabular text-destructive">
                {s().exit_code}
              </span>
            </Show>
            <div class="ml-auto flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground">
              <Show when={s().has_health}>
                <Tooltip>
                  <TooltipTrigger class="flex items-center" as="span">
                    <span class={cn("size-2 rounded-full", healthDot(s().has_health, s().health))} />
                  </TooltipTrigger>
                  <TooltipContent>health: {s().health}</TooltipContent>
                </Tooltip>
              </Show>
              <Show when={portBinding()}>
                {(p) => (
                  <span
                    class="rounded bg-primary/10 px-1 font-mono text-[10.5px] font-medium tabular text-primary"
                    title={`${p().ip}:${p().port}`}
                  >
                    :{p().port}
                  </span>
                )}
              </Show>
              <Show when={s().pid > 0}>
                <span class="font-mono tabular opacity-70">{s().pid}</span>
              </Show>
            </div>
          </button>

          <div
            class={cn(
              "absolute right-1 top-1/2 flex -translate-y-1/2 items-center transition-opacity duration-100",
              selected()
                ? "opacity-100"
                : "pointer-events-none opacity-0 group-hover/svc:pointer-events-auto group-hover/svc:opacity-100"
            )}
          >
            <DropdownMenu>
              <DropdownMenuTrigger
                as={Button}
                variant="secondary"
                size="icon-sm"
                class="border border-border shadow-sm"
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
                        <DropdownMenuShortcut>r</DropdownMenuShortcut>
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => stopService(props.project, props.name)}>
                        <Power />
                        Stop
                        <DropdownMenuShortcut>s</DropdownMenuShortcut>
                      </DropdownMenuItem>
                    </>
                  }
                >
                  <DropdownMenuItem onSelect={() => startService(props.project, props.name)}>
                    <Play />
                    Start
                  </DropdownMenuItem>
                </Show>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  class="text-destructive focus:text-destructive [&>svg]:text-destructive"
                  onSelect={() => killService(props.project, props.name)}
                >
                  <Skull />
                  Kill
                  <DropdownMenuShortcut>k k</DropdownMenuShortcut>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      )}
    </Show>
  );
}

function ActionRow(props: { project: string; name: string }) {
  const state = createMemo(() =>
    (actionStatesMap()[props.project] ?? []).find((a) => a.name === props.name)
  );
  const selected = () =>
    selectedAction() === props.name && selectedProject() === props.project;
  const cursor = () =>
    sameNavItem(keyboardCursor(), { kind: "action", project: props.project, action: props.name });
  const status = () => state()?.status ?? "idle";

  return (
    <div class="group/act relative flex items-stretch" data-kbd-cursor={cursor() ? "" : undefined}>
      <button
        type="button"
        onClick={() => selectAction(props.project, props.name)}
        class={cn(
          "flex h-[30px] min-w-0 flex-1 items-center gap-2 py-0 pl-8 pr-2.5 text-left text-[13px]",
          "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring",
          rowClasses(selected(), cursor())
        )}
      >
        <span
          class={cn(
            "size-1.5 shrink-0 rounded-full",
            status() === "idle" ? "bg-muted-foreground/50" : statusDot(status())
          )}
        />
        <span class="truncate">{props.name}</span>
        <Show when={status() === "exited" && (state()?.exit_code ?? 0) !== 0}>
          <span class="shrink-0 font-mono text-[11px] tabular text-destructive">
            {state()?.exit_code}
          </span>
        </Show>
        <Show when={(state()?.pid ?? 0) > 0}>
          <span class="ml-auto shrink-0 font-mono text-[11px] tabular text-muted-foreground opacity-70">
            {state()?.pid}
          </span>
        </Show>
      </button>

      <div
        class={cn(
          "absolute right-1 top-1/2 flex -translate-y-1/2 items-center transition-opacity duration-100",
          selected()
            ? "opacity-100"
            : "pointer-events-none opacity-0 group-hover/act:pointer-events-auto group-hover/act:opacity-100"
        )}
      >
        <Button
          variant="secondary"
          size="icon-sm"
          class="border border-border shadow-sm"
          title={`Run ${props.name}`}
          onClick={(e: MouseEvent) => {
            e.stopPropagation();
            selectAction(props.project, props.name);
            runAction(props.project, props.name);
          }}
        >
          <Play class="!size-3 text-primary" />
          <span class="sr-only">Run</span>
        </Button>
      </div>
    </div>
  );
}

function ProjectItem(props: { name: string }) {
  const project = createMemo(() => projectsList().find((p) => p.name === props.name));
  const serviceList = createMemo(() => servicesMap()[props.name]);
  const active = () => selectedProject() === props.name;
  const selected = () => active() && selectedService() === null && selectedAction() === null;
  const cursor = () => sameNavItem(keyboardCursor(), { kind: "project", project: props.name });

  const aggregateHealth = createMemo(() => {
    const withHealth = (serviceList() ?? []).filter((s) => s.has_health);
    if (withHealth.length === 0) return "";
    if (withHealth.some((s) => s.health === "unhealthy")) return "unhealthy";
    if (withHealth.some((s) => s.health === "starting" || s.health === "starting_healthy")) return "starting";
    if (withHealth.every((s) => s.health === "healthy")) return "healthy";
    return "";
  });
  const actionList = createMemo(() => actionsMap()[props.name] ?? []);

  return (
    <Show when={project()}>
      {(p) => (
        <Collapsible
          open={isProjectExpanded(props.name)}
          onOpenChange={(open) => setProjectExpanded(props.name, open)}
        >
          <div
            data-kbd-cursor={cursor() ? "" : undefined}
            class={cn(
              "group/proj relative flex items-center border-l-2",
              active() ? "border-primary" : "border-transparent",
              selected()
                ? "bg-accent text-accent-foreground"
                : "hover:bg-muted/70",
              !selected() && cursor() && "bg-muted"
            )}
          >
            <CollapsibleTrigger class="flex h-8 w-6 shrink-0 items-center justify-center text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring [&[data-expanded]_svg]:rotate-90">
              <ChevronRight class="size-3.5 transition-transform duration-100" />
              <span class="sr-only">Toggle</span>
            </CollapsibleTrigger>
            <button
              type="button"
              onClick={() => selectProject(props.name)}
              class="flex h-8 min-w-0 flex-1 items-center gap-2 pr-1 text-left text-[13px] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
            >
              <Boxes class="size-3.5 shrink-0 text-muted-foreground" />
              <span class="truncate font-medium">{props.name}</span>
              <div class="ml-auto flex shrink-0 items-center gap-1.5 pr-1">
                <Show when={aggregateHealth()}>
                  <Tooltip>
                    <TooltipTrigger as="span" class="flex items-center">
                      <span class={cn("size-2 rounded-full", healthDot(true, aggregateHealth()))} />
                    </TooltipTrigger>
                    <TooltipContent>health: {aggregateHealth()}</TooltipContent>
                  </Tooltip>
                </Show>
                <Badge variant={statusTone(p().status)}>{p().status}</Badge>
              </div>
            </button>

            {/* Hover actions */}
            <div
              class={cn(
                "absolute right-1 top-1/2 flex -translate-y-1/2 items-center gap-0.5 rounded-md bg-card/95 p-0.5 shadow-sm ring-1 ring-border backdrop-blur transition-opacity duration-100",
                "opacity-0 pointer-events-none group-hover/proj:pointer-events-auto group-hover/proj:opacity-100 focus-within:pointer-events-auto focus-within:opacity-100"
              )}
            >
              <Tooltip>
                <TooltipTrigger
                  as={Button}
                  variant="ghost"
                  size="icon-sm"
                  class="text-muted-foreground hover:text-foreground"
                  onClick={(e: MouseEvent) => {
                    e.stopPropagation();
                    openGitView(props.name);
                  }}
                >
                  <GitBranch />
                  <span class="sr-only">Git log</span>
                </TooltipTrigger>
                <TooltipContent>Git history (g)</TooltipContent>
              </Tooltip>
              <Show
                when={p().status !== "stopped"}
                fallback={
                  <Tooltip>
                    <TooltipTrigger
                      as={Button}
                      variant="ghost"
                      size="icon-sm"
                      class="text-success hover:text-success"
                      onClick={(e: MouseEvent) => {
                        e.stopPropagation();
                        startProject(props.name);
                      }}
                    >
                      <Play />
                      <span class="sr-only">Start project</span>
                    </TooltipTrigger>
                    <TooltipContent>Start project (u)</TooltipContent>
                  </Tooltip>
                }
              >
                <>
                  <Tooltip>
                    <TooltipTrigger
                      as={Button}
                      variant="ghost"
                      size="icon-sm"
                      class="text-muted-foreground hover:text-foreground"
                      onClick={(e: MouseEvent) => {
                        e.stopPropagation();
                        startProject(props.name);
                      }}
                    >
                      <RefreshCw />
                      <span class="sr-only">Reload config</span>
                    </TooltipTrigger>
                    <TooltipContent>Reload config & prune orphans</TooltipContent>
                  </Tooltip>
                  <Tooltip>
                    <TooltipTrigger
                      as={Button}
                      variant="ghost"
                      size="icon-sm"
                      class="text-muted-foreground hover:text-destructive"
                      onClick={(e: MouseEvent) => {
                        e.stopPropagation();
                        stopProject(props.name);
                      }}
                    >
                      <Power />
                      <span class="sr-only">Stop project</span>
                    </TooltipTrigger>
                    <TooltipContent>Stop project (d d)</TooltipContent>
                  </Tooltip>
                </>
              </Show>
            </div>
          </div>
          <CollapsibleContent>
            <div class="ml-3 border-l py-0.5 pl-1">
              <Show
                when={serviceList() !== undefined}
                fallback={<p class="px-4 py-1 text-xs text-muted-foreground">loading…</p>}
              >
                <Show
                  when={(serviceList() ?? []).length > 0}
                  fallback={
                    <p class="px-4 py-1 text-xs text-muted-foreground">no services</p>
                  }
                >
                  <For each={serviceList()}>
                    {(s) => <ServiceRow project={props.name} name={s.name} />}
                  </For>
                </Show>
              </Show>
              <Show when={actionList().length > 0}>
                <div class="my-1 border-t pt-1">
                  <For each={actionList()}>
                    {(a) => <ActionRow project={props.name} name={a.name} />}
                  </For>
                </div>
              </Show>
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
    </Show>
  );
}

export function Sidebar() {
  const projectNames = createMemo(() => projectsList().map((p) => p.name));

  return (
    <>
      {/* Mobile backdrop: tap to dismiss the drawer. */}
      <Show when={sidebarOpen()}>
        <div
          class="fixed inset-0 z-30 bg-black/50 backdrop-blur-[2px] md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      </Show>
      <aside
        class={cn(
          "flex h-full w-72 shrink-0 flex-col border-r bg-card",
          // Drawer on phones, static column on desktop.
          "max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-40 max-md:max-w-[85vw] max-md:shadow-lg max-md:transition-transform max-md:duration-200"
        )}
        classList={{ "max-md:-translate-x-full": !sidebarOpen() }}
      >
      {/* Brand header */}
      <header class="flex h-12 shrink-0 items-center justify-between border-b px-3">
        <button
          type="button"
          onClick={() => {
            fetchDaemonStatus();
            setShowDaemonModal(true);
          }}
          class="flex min-w-0 items-center gap-2 rounded-md px-1 py-1 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <span class="flex size-6 items-center justify-center rounded-md bg-primary/15 text-primary">
            <Boxes class="size-3.5" />
          </span>
          <span class="truncate text-[13px] font-semibold tracking-tight">local-compose</span>
        </button>

        <div class="flex items-center gap-0.5">
          <Button
            variant="ghost"
            size="xs"
            onClick={() => {
              fetchDaemonStatus();
              setShowDaemonModal(true);
            }}
            class="text-muted-foreground"
            title="Daemon status"
          >
            <WsDot />
            <span class="capitalize">{wsStatus()}</span>
            <Show when={daemonInfo()?.pid}>
              <span class="font-mono tabular opacity-70">:{daemonInfo()?.pid}</span>
            </Show>
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => setSidebarOpen(false)}
            class="text-muted-foreground md:hidden"
            aria-label="Close menu"
          >
            <X class="size-3.5" />
          </Button>
        </div>
      </header>

      {/* Project list */}
      <nav class="flex-1 overflow-y-auto overflow-x-hidden py-2 outline-none" data-sidebar-nav tabindex="-1">
        <div class="flex items-center justify-between px-3 pb-1.5 pt-1">
          <p class="text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground">
            Projects
          </p>
          <Button
            variant="ghost"
            size="xs"
            onClick={() => setShowAddProject(true)}
            class="h-5 px-1.5 text-[11px] text-muted-foreground"
          >
            <Plus class="size-3" />
            Add
          </Button>
        </div>
        <div class="flex flex-col px-1.5">
          <For each={projectNames()}>{(name) => <ProjectItem name={name} />}</For>
        </div>
        <Show when={projectNames().length === 0}>
          <p class="px-4 py-4 text-xs leading-relaxed text-muted-foreground">
            No projects yet. Add one above or start with{" "}
            <code class="rounded bg-muted px-1 font-mono text-foreground">local-compose up</code>.
          </p>
        </Show>
      </nav>

      {/* Footer */}
      <footer class="flex h-10 shrink-0 items-center justify-between border-t px-3">
        <Button
          variant="ghost"
          size="xs"
          onClick={() => toggleHelp()}
          class="text-muted-foreground"
        >
          <Kbd>?</Kbd>
          <span>shortcuts</span>
        </Button>
        <ThemeToggle />
      </footer>
      </aside>
    </>
  );
}

function ThemeToggle() {
  return (
    <Switch checked={theme() === "dark"} onChange={(v) => setTheme(v ? "dark" : "light")} aria-label="Dark mode" />
  );
}
