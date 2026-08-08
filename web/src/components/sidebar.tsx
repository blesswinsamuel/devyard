import { For, Show, createMemo, createSignal } from "solid-js";
import { ChevronRight, Boxes, MoreVertical, Play, Plus, Power, RefreshCw, RotateCcw, Skull, Sun, Moon, X, GitBranch } from "lucide-solid";
import {
  projects,
  services,
  actions,
  actionStates,
  isProjectExpanded,
  selectedProject,
  selectedService,
  selectedAction,
  keyboardCursor,
  sameNavItem,
  navItemKey,
  setProjectExpanded,
  selectProject,
  selectService,
  selectAction,
  startProject,
  startProjectByPath,
  stopProject,
  restartService,
  startService,
  stopService,
  killService,
  runAction,
  openGitView,
  theme,
  setTheme,
  wsStatus,
  toggleHelp,
  daemonInfo,
  fetchDaemonStatus,
  restartDaemon,
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
                  <DropdownMenuItem onSelect={() => startService(props.project, props.name)}>
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

function ActionRow(props: { project: string; name: string }) {
  const actionState = createMemo(() =>
    (actionStates()[props.project] ?? []).find((a) => a.name === props.name)
  );
  const selected = () =>
    selectedAction() === props.name && selectedProject() === props.project;
  const cursor = () =>
    sameNavItem(keyboardCursor(), {
      kind: "action",
      project: props.project,
      action: props.name,
    });

  const status = () => actionState()?.status ?? "idle";
  const pid = () => actionState()?.pid ?? 0;
  const exitCode = () => actionState()?.exit_code ?? 0;

  return (
    <div
      class="group/act relative flex items-stretch"
      data-nav-key={navItemKey({
        kind: "action",
        project: props.project,
        action: props.name,
      })}
      data-kbd-cursor={cursor() ? "" : undefined}
    >
      <button
        type="button"
        onClick={() => selectAction(props.project, props.name)}
        class={cn(
          "flex h-7 min-w-0 flex-1 items-center gap-2 px-3 pl-7 pr-9 text-left text-sm transition-colors",
          "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
          selected() && "bg-muted text-foreground",
          cursor() && !selected() && "bg-muted/40 ring-1 ring-inset ring-ring/60",
          cursor() && selected() && "ring-1 ring-inset ring-ring"
        )}
      >
        <span class={cn("inline-block size-1.5 shrink-0", status() === "idle" ? "bg-muted-foreground/60" : statusDot(status()))} />
        <span class="truncate font-medium">{props.name}</span>
        <Show when={status() === "exited" && exitCode() !== 0}>
          <span class="shrink-0 text-xs text-destructive">({exitCode()})</span>
        </Show>
        <div class="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <Show when={pid() > 0}>
            <span class="font-mono text-[11px] opacity-80">{pid()}</span>
          </Show>
        </div>
      </button>

      <div
        class={cn(
          "absolute right-1 top-1/2 flex -translate-y-1/2 items-center transition-opacity",
          selected()
            ? "opacity-100"
            : "opacity-0 pointer-events-none group-hover/act:opacity-100 group-hover/act:pointer-events-auto"
        )}
      >
        <Button
          variant="ghost"
          size="icon-sm"
          class="border border-transparent bg-card/80 hover:border-border"
          title={`Run action ${props.name}`}
          onClick={(e) => {
            e.stopPropagation();
            selectAction(props.project, props.name);
            runAction(props.project, props.name);
          }}
        >
          <Play class="size-3 text-primary" />
          <span class="sr-only">Run</span>
        </Button>
      </div>
    </div>
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

  const actionList = createMemo(() => actions()[props.name] ?? []);

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
                    openGitView(props.name);
                  }}
                >
                  <GitBranch />
                  <span class="sr-only">Git log</span>
                </TooltipTrigger>
                <TooltipContent>View git log</TooltipContent>
              </Tooltip>
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
                    <TooltipContent>Start project (u)</TooltipContent>
                  </Tooltip>
                }
              >
                <div class="flex items-center gap-0.5">
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
                      <RefreshCw />
                      <span class="sr-only">Reload config</span>
                    </TooltipTrigger>
                    <TooltipContent>Reload config & prune orphans (up)</TooltipContent>
                  </Tooltip>
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
                </div>
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
              <Show when={actionList().length > 0}>
                <div class="border-t border-border/50 my-1">
                  <For each={actionList()}>
                    {(act) => <ActionRow project={props.name} name={act.name} />}
                  </For>
                </div>
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

function AddProjectModal(props: { open: boolean; onClose: () => void }) {
  const [configPath, setConfigPath] = createSignal("");
  const [envFile, setEnvFile] = createSignal("");

  const handleSubmit = (e: SubmitEvent) => {
    e.preventDefault();
    const path = configPath().trim();
    if (!path) return;
    startProjectByPath(path, envFile().trim() || undefined);
    setConfigPath("");
    setEnvFile("");
    props.onClose();
  };

  return (
    <Show when={props.open}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm p-4"
        onClick={(e) => {
          if (e.target === e.currentTarget) props.onClose();
        }}
      >
        <div class="w-full max-w-md rounded-lg border border-border bg-card p-5 shadow-lg">
          <div class="flex items-center justify-between pb-3 border-b border-border mb-4">
            <h3 class="font-semibold text-base">Add Project</h3>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => props.onClose()}
            >
              <X class="size-4" />
            </Button>
          </div>
          <form onSubmit={handleSubmit} class="space-y-4">
            <div class="space-y-1.5">
              <label class="text-xs font-medium text-muted-foreground">
                Config Path (local-compose.yml)
              </label>
              <input
                type="text"
                required
                value={configPath()}
                onInput={(e) => setConfigPath(e.currentTarget.value)}
                placeholder="/path/to/local-compose.yml"
                class="w-full rounded-md border border-input bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>
            <div class="space-y-1.5">
              <label class="text-xs font-medium text-muted-foreground">
                Env File (Optional)
              </label>
              <input
                type="text"
                value={envFile()}
                onInput={(e) => setEnvFile(e.currentTarget.value)}
                placeholder="/path/to/.env"
                class="w-full rounded-md border border-input bg-background px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>
            <div class="flex justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => props.onClose()}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm">
                Add & Start
              </Button>
            </div>
          </form>
        </div>
      </div>
    </Show>
  );
}

function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatUptime(startTimeStr?: string): string {
  if (!startTimeStr) return "N/A";
  const start = new Date(startTimeStr).getTime();
  if (isNaN(start)) return "N/A";
  const diffSec = Math.max(0, Math.floor((Date.now() - start) / 1000));
  if (diffSec < 60) return `${diffSec}s`;
  const mins = Math.floor(diffSec / 60);
  if (mins < 60) return `${mins}m ${diffSec % 60}s`;
  const hours = Math.floor(mins / 60);
  return `${hours}h ${mins % 60}m`;
}

function DaemonStatusModal(props: { open: boolean; onClose: () => void }) {
  const [restarting, setRestarting] = createSignal(false);

  const handleRestart = () => {
    setRestarting(true);
    restartDaemon();
    setTimeout(() => {
      setRestarting(false);
      props.onClose();
    }, 2000);
  };

  return (
    <Show when={props.open}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm p-4"
        onClick={(e) => {
          if (e.target === e.currentTarget) props.onClose();
        }}
      >
        <div class="w-full max-w-md rounded-lg border border-border bg-card p-5 shadow-lg">
          <div class="flex items-center justify-between pb-3 border-b border-border mb-4">
            <div class="flex items-center gap-2">
              <Boxes class="size-5 text-primary" />
              <h3 class="font-semibold text-base">Daemon Status</h3>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => props.onClose()}
            >
              <X class="size-4" />
            </Button>
          </div>

          <div class="space-y-3 text-sm">
            <div class="flex justify-between items-center py-1 border-b border-border/40">
              <span class="text-muted-foreground">Status</span>
              <span class="flex items-center gap-1.5 font-medium">
                <WsDot />
                <span class="capitalize">{wsStatus()}</span>
              </span>
            </div>

            <Show when={daemonInfo()}>
              {(info) => (
                <>
                  <div class="flex justify-between items-center py-1 border-b border-border/40">
                    <span class="text-muted-foreground">Process ID (PID)</span>
                    <span class="font-mono">{info().pid}</span>
                  </div>
                  <div class="flex justify-between items-center py-1 border-b border-border/40">
                    <span class="text-muted-foreground">Uptime</span>
                    <span class="font-mono">{formatUptime(info().start_time)}</span>
                  </div>
                  <div class="flex justify-between items-center py-1 border-b border-border/40">
                    <span class="text-muted-foreground">Goroutines</span>
                    <span class="font-mono">{info().goroutines}</span>
                  </div>
                  <div class="flex justify-between items-center py-1 border-b border-border/40">
                    <span class="text-muted-foreground">Memory (Alloc / RSS)</span>
                    <span class="font-mono">
                      {formatBytes(info().memory_alloc)} / {formatBytes(info().memory_rss)}
                    </span>
                  </div>
                  <div class="flex justify-between items-center py-1 border-b border-border/40">
                    <span class="text-muted-foreground">Go Version</span>
                    <span class="font-mono text-xs">{info().go_version}</span>
                  </div>
                </>
              )}
            </Show>
          </div>

          <div class="flex justify-between items-center pt-5">
            <Button
              variant="outline"
              size="sm"
              onClick={() => fetchDaemonStatus()}
              title="Refresh daemon status"
            >
              <RefreshCw class="size-3.5 mr-1" />
              Refresh Stats
            </Button>

            <Button
              variant="destructive"
              size="sm"
              disabled={restarting()}
              onClick={handleRestart}
            >
              <RotateCcw class="size-3.5 mr-1" />
              {restarting() ? "Restarting..." : "Restart Daemon"}
            </Button>
          </div>
        </div>
      </div>
    </Show>
  );
}

export function Sidebar() {
  const projectNames = createMemo(() => projects().map((p) => p.name));
  const [showAddModal, setShowAddModal] = createSignal(false);
  const [showDaemonModal, setShowDaemonModal] = createSignal(false);

  return (
    <aside class="flex h-full w-72 shrink-0 flex-col border-r border-border bg-card">
      <header class="flex h-12 shrink-0 items-center justify-between border-b border-border px-3">
        <button
          type="button"
          onClick={() => {
            fetchDaemonStatus();
            setShowDaemonModal(true);
          }}
          class="flex items-center gap-2 text-left hover:opacity-80 focus-visible:outline-none"
          title="View Daemon Status"
        >
          <Boxes class="size-5 text-primary" />
          <span class="font-semibold tracking-wide">local-compose</span>
        </button>

        <button
          type="button"
          onClick={() => {
            fetchDaemonStatus();
            setShowDaemonModal(true);
          }}
          class="flex items-center gap-1.5 rounded px-1.5 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none"
          title="Daemon Status & Settings"
        >
          <WsDot />
          <span class="capitalize">{wsStatus()}</span>
          <Show when={daemonInfo()?.pid}>
            <span class="font-mono text-[10px] text-muted-foreground/70">pid:{daemonInfo()?.pid}</span>
          </Show>
        </button>
      </header>

      <nav
        class="flex-1 overflow-y-auto py-2 outline-none"
        data-sidebar-nav
        tabindex="-1"
      >
        <div class="flex items-center justify-between px-4 pb-1">
          <p class="text-xs uppercase tracking-wider text-muted-foreground">
            Projects
          </p>
          <button
            type="button"
            onClick={() => setShowAddModal(true)}
            class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none"
            title="Add Project"
          >
            <Plus class="size-3.5" />
            <span>Add Project</span>
          </button>
        </div>
        <For each={projectNames()}>
          {(name) => <ProjectItem name={name} />}
        </For>
        <Show when={projectNames().length === 0}>
          <p class="px-4 py-4 text-xs leading-relaxed text-muted-foreground">
            No projects running. Add one above or start with <code class="text-foreground">local-compose up</code>.
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

      <AddProjectModal open={showAddModal()} onClose={() => setShowAddModal(false)} />
      <DaemonStatusModal open={showDaemonModal()} onClose={() => setShowDaemonModal(false)} />
    </aside>
  );
}
