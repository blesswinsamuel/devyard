import { For, Show, createEffect, createMemo } from "solid-js";
import {
  Boxes,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  ExternalLink,
  GitBranch,
  Globe,
  History,
  MoreVertical,
  Play,
  Plus,
  Power,
  RefreshCw,
  RotateCcw,
  Settings,
  Skull,
  Square,
  Terminal,
  X,
} from "lucide-solid";
import {
  isProjectExpanded,
  keyboardCursor,
  navItemKey,
  sameNavItem,
  selectProject,
  selectService,
  selectTask,
  selectedProject,
  selectedService,
  selectedTask,
  setProjectExpanded,
} from "~/stores/nav";
import {
  daemonInfo,
  fetchDaemonStatus,
  gitStatuses,
  killService,
  ports,
  projects as projectsList,
  refreshAll,
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
import { isPreviousLogs, tabKey, togglePreviousLogs } from "~/stores/logs";
import { pushToast, openPortsDialog, setShowAddProject, setShowDaemonModal, setShowSettingsModal, theme, setTheme } from "~/stores/app";
import { toggleHelp, sidebarOpen, setSidebarOpen } from "~/stores/app";
import { openGitView } from "~/stores/nav";
import { eventStatus } from "~/lib/events";
import { healthDot, statusDot } from "~/lib/status";
import { cn } from "~/lib/utils";
import { Kbd } from "~/components/ui/kbd";
import { Button, buttonVariants } from "~/components/ui/button";
import { cleanProxyUrl } from "~/lib/format";
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
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "~/components/ui/context-menu";

function copyToClipboard(text: string, label: string) {
  navigator.clipboard.writeText(text).then(
    () => pushToast(`Copied ${label} to clipboard`, "info"),
    () => pushToast(`Failed to copy to clipboard`, "error")
  );
}

function WsDot() {
  return (
    <span
      class={cn("inline-block size-2 shrink-0 rounded-full", {
        "bg-success shadow-[0_0_6px_var(--success)]": eventStatus() === "open",
        "bg-warning animate-pulse": eventStatus() === "connecting",
        "bg-destructive": eventStatus() === "closed",
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
  const showingPrevLogs = () =>
    isPreviousLogs(tabKey(props.project, "service", props.name));

  return (
    <Show when={service()}>
      {(s) => (
        <ContextMenu>
          <ContextMenuTrigger
            as="div"
            class="group/svc relative flex items-stretch"
            data-kbd-cursor={cursor() ? "" : undefined}
            onContextMenu={() => selectService(props.project, props.name)}
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
              <Show when={s().status === "exited" && (s().exitCode ?? (s() as any).exit_code ?? 0) !== 0}>
                <span class="shrink-0 font-mono text-[11px] tabular text-destructive">
                  {s().exitCode ?? (s() as any).exit_code}
                </span>
              </Show>
              <div class="ml-auto flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                <Show when={(s().hasHealth ?? (s() as any).has_health) && (s().status === "running" || s().status === "starting")}>
                  <Tooltip>
                    <TooltipTrigger class="flex items-center" as="span">
                      <span class={cn("size-2 rounded-full", healthDot(s().hasHealth ?? (s() as any).has_health, s().health))} />
                    </TooltipTrigger>
                    <TooltipContent>health: {s().health}</TooltipContent>
                  </Tooltip>
                </Show>
                <Show when={s().pid > 0}>
                  <span class="font-mono tabular opacity-70">{s().pid}</span>
                </Show>
              </div>
            </button>

            <div
              class={cn(
                "absolute right-1 top-1/2 flex -translate-y-1/2 items-center gap-0.5 transition-opacity duration-100",
                selected()
                  ? "opacity-100"
                  : "pointer-events-none opacity-0 group-hover/svc:pointer-events-auto group-hover/svc:opacity-100"
              )}
            >
              <Show when={(s().proxyUrls?.length ?? 0) > 0}>
                {() => (
                  <Tooltip>
                    <TooltipTrigger
                      as="a"
                      href={s().proxyUrls[0]}
                      target="_blank"
                      rel="noreferrer"
                      class={cn(
                        buttonVariants({ variant: "secondary", size: "icon-sm" }),
                        "border border-border shadow-sm text-muted-foreground hover:text-foreground"
                      )}
                      onClick={(e: MouseEvent) => e.stopPropagation()}
                    >
                      <ExternalLink class="!size-3" />
                      <span class="sr-only">Open in browser</span>
                    </TooltipTrigger>
                    <TooltipContent>Open {s().proxyUrls[0]}</TooltipContent>
                  </Tooltip>
                )}
              </Show>
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
                  <Show when={(s().proxyUrls?.length ?? 0) > 0}>
                    <DropdownMenuSeparator />
                    <For each={s().proxyUrls}>
                      {(url) => (
                        <>
                          <DropdownMenuItem onSelect={() => window.open(url, "_blank")}>
                            <ExternalLink />
                            Open {cleanProxyUrl(url)} in Browser
                          </DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => copyToClipboard(url, "proxy URL")}>
                            <Copy />
                            Copy URL ({cleanProxyUrl(url)})
                          </DropdownMenuItem>
                        </>
                      )}
                    </For>
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
          </ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem onSelect={() => selectService(props.project, props.name)}>
              <Terminal />
              View Logs
            </ContextMenuItem>
            <ContextMenuSeparator />
            <Show
              when={s().status === "stopped" || s().status === "exited"}
              fallback={
                <>
                  <ContextMenuItem onSelect={() => restartService(props.project, props.name)}>
                    <RotateCcw />
                    Restart
                    <ContextMenuShortcut>r</ContextMenuShortcut>
                  </ContextMenuItem>
                  <ContextMenuItem onSelect={() => stopService(props.project, props.name)}>
                    <Power />
                    Stop
                    <ContextMenuShortcut>s</ContextMenuShortcut>
                  </ContextMenuItem>
                </>
              }
            >
              <ContextMenuItem onSelect={() => startService(props.project, props.name)}>
                <Play />
                Start
              </ContextMenuItem>
            </Show>
            <ContextMenuSeparator />
            <ContextMenuItem
              variant="destructive"
              onSelect={() => killService(props.project, props.name)}
            >
              <Skull />
              Kill
              <ContextMenuShortcut>k k</ContextMenuShortcut>
            </ContextMenuItem>
            <Show when={(s().proxyUrls?.length ?? 0) > 0}>
              <ContextMenuSeparator />
              <For each={s().proxyUrls}>
                {(url) => (
                  <>
                    <ContextMenuItem onSelect={() => window.open(url, "_blank")}>
                      <ExternalLink />
                      Open {cleanProxyUrl(url)} in Browser
                    </ContextMenuItem>
                    <ContextMenuItem onSelect={() => copyToClipboard(url, "proxy URL")}>
                      <Copy />
                      Copy URL ({cleanProxyUrl(url)})
                    </ContextMenuItem>
                  </>
                )}
              </For>
            </Show>
            <ContextMenuSeparator />
            <ContextMenuItem
              onSelect={() => togglePreviousLogs(tabKey(props.project, "service", props.name))}
            >
              <History />
              {showingPrevLogs() ? "Show Live Logs" : "Show Previous Run"}
              <ContextMenuShortcut>p</ContextMenuShortcut>
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => copyToClipboard(props.name, "service name")}>
              <Copy />
              Copy Name
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
      )}
    </Show>
  );
}

function TaskRow(props: { project: string; name: string }) {
  const task = createMemo(() =>
    (tasksMap()[props.project] ?? []).find((t) => t.name === props.name)
  );
  const selected = () =>
    selectedTask() === props.name && selectedProject() === props.project;
  const cursor = () =>
    sameNavItem(keyboardCursor(), { kind: "task", project: props.project, task: props.name });
  const status = () => task()?.status ?? "idle";
  const showingPrevLogs = () =>
    isPreviousLogs(tabKey(props.project, "task", props.name));

  return (
    <ContextMenu>
      <ContextMenuTrigger
        as="div"
        class="group/act relative flex items-stretch"
        data-kbd-cursor={cursor() ? "" : undefined}
        onContextMenu={() => selectTask(props.project, props.name)}
      >
        <button
          type="button"
          onClick={() => selectTask(props.project, props.name)}
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
          <Show when={status() === "exited" && (task()?.exitCode ?? (task() as any)?.exit_code ?? 0) !== 0}>
            <span class="shrink-0 font-mono text-[11px] tabular text-destructive">
              {task()?.exitCode ?? (task() as any)?.exit_code}
            </span>
          </Show>
          <Show when={(task()?.pid ?? 0) > 0}>
            <span class="ml-auto shrink-0 font-mono text-[11px] tabular text-muted-foreground opacity-70">
              {task()?.pid}
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
          <Show
            when={status() === "running" || status() === "starting"}
            fallback={
              <Button
                variant="secondary"
                size="icon-sm"
                class="border border-border shadow-sm"
                title={`Run task ${props.name}`}
                onClick={(e: MouseEvent) => {
                  e.stopPropagation();
                  selectTask(props.project, props.name);
                  runTask(props.project, props.name);
                }}
              >
                <Play class="!size-3 text-primary" />
                <span class="sr-only">Run</span>
              </Button>
            }
          >
            <Button
              variant="secondary"
              size="icon-sm"
              class="border border-border shadow-sm"
              title={`Stop task ${props.name}`}
              onClick={(e: MouseEvent) => {
                e.stopPropagation();
                stopTask(props.project, props.name);
              }}
            >
              <Square class="!size-3 text-destructive" />
              <span class="sr-only">Stop</span>
            </Button>
          </Show>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <Show
          when={status() === "running" || status() === "starting"}
          fallback={
            <ContextMenuItem
              onSelect={() => {
                selectTask(props.project, props.name);
                runTask(props.project, props.name);
              }}
            >
              <Play />
              Run Task
            </ContextMenuItem>
          }
        >
          <ContextMenuItem onSelect={() => stopTask(props.project, props.name)}>
            <Square />
            Stop Task
          </ContextMenuItem>
          <ContextMenuItem
            variant="destructive"
            onSelect={() => killService(props.project, props.name)}
          >
            <Skull />
            Kill Task
          </ContextMenuItem>
        </Show>
        <ContextMenuItem onSelect={() => selectTask(props.project, props.name)}>
          <Terminal />
          View Logs
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem
          onSelect={() => togglePreviousLogs(tabKey(props.project, "task", props.name))}
        >
          <History />
          {showingPrevLogs() ? "Show Live Logs" : "Show Previous Run"}
          <ContextMenuShortcut>p</ContextMenuShortcut>
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onSelect={() => copyToClipboard(props.name, "task name")}>
          <Copy />
          Copy Name
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function ProjectStatusDot(props: { status: string }) {
  return (
    <Tooltip>
      <TooltipTrigger as="span" class="flex items-center">
        <span
          class={cn("size-2 shrink-0 rounded-full", {
            "bg-success shadow-[0_0_6px_var(--success)]": props.status === "running",
            "bg-warning animate-pulse": props.status === "stopping" || props.status === "starting",
            "bg-muted-foreground/35": props.status === "stopped",
          })}
        />
      </TooltipTrigger>
      <TooltipContent>Project: {props.status}</TooltipContent>
    </Tooltip>
  );
}

function GitPromptBadge(props: { project: string }) {
  const s = createMemo(() => gitStatuses()[props.project]);

  const hasAheadBehind = () => (s()?.ahead ?? 0) > 0 || (s()?.behind ?? 0) > 0;
  const hasWorktreeChanges = () =>
    (s()?.staged ?? 0) > 0 || (s()?.dirty ?? 0) > 0 || (s()?.untracked ?? 0) > 0 || (s()?.conflicts ?? 0) > 0;
  const isClean = () => !hasAheadBehind() && !hasWorktreeChanges();

  const branchLabel = () => {
    const st = s();
    return st?.branch && st.branch !== "(detached)"
      ? st.branch
      : st?.headHash
      ? `detached:${st.headHash.slice(0, 7)}`
      : "HEAD";
  };

  return (
    <Show when={s()?.isRepo}>
      <Tooltip>
        <TooltipTrigger
          as={Button}
          variant="ghost"
          size="xs"
          aria-label={`Git status for ${props.project}`}
          onClick={(e: MouseEvent) => {
            e.stopPropagation();
            openGitView(props.project);
          }}
          class={cn(
            "h-5 !cursor-default gap-1 rounded px-1.5 py-0 font-mono text-[11px] leading-none transition-colors select-none",
            isClean()
              ? "text-muted-foreground/60 hover:bg-muted hover:text-foreground"
              : "bg-muted/70 text-foreground hover:bg-muted font-medium"
          )}
        >
          <GitBranch class="size-3 shrink-0 pointer-events-none text-muted-foreground" />
          <Show when={isClean()}>
            <span class="pointer-events-none text-[10px] text-muted-foreground">✓</span>
          </Show>
          <Show when={(s()?.ahead ?? 0) > 0}>
            <span class="pointer-events-none text-sky-500 font-semibold">⇡{s()?.ahead}</span>
          </Show>
          <Show when={(s()?.behind ?? 0) > 0}>
            <span class="pointer-events-none text-amber-500 font-semibold">⇣{s()?.behind}</span>
          </Show>
          <Show when={(s()?.staged ?? 0) > 0}>
            <span class="pointer-events-none text-emerald-500 font-semibold">+{s()?.staged}</span>
          </Show>
          <Show when={(s()?.dirty ?? 0) > 0}>
            <span class="pointer-events-none text-amber-500 font-semibold">!{s()?.dirty}</span>
          </Show>
          <Show when={(s()?.untracked ?? 0) > 0}>
            <span class="pointer-events-none text-muted-foreground">?{s()?.untracked}</span>
          </Show>
          <Show when={(s()?.conflicts ?? 0) > 0}>
            <span class="pointer-events-none text-destructive font-bold">×{s()?.conflicts}</span>
          </Show>
        </TooltipTrigger>
        <TooltipContent class="max-w-xs !cursor-default flex-col items-start p-2 text-xs">
          <div class="flex items-center gap-1.5 font-medium">
            <GitBranch class="size-3.5 text-primary" />
            <span>{branchLabel()}</span>
            <Show when={hasAheadBehind()}>
              <span class="text-background/65 font-normal">
                ({[
                  (s()?.ahead ?? 0) > 0 ? `${s()?.ahead} ahead` : "",
                  (s()?.behind ?? 0) > 0 ? `${s()?.behind} behind` : "",
                ].filter(Boolean).join(", ")})
              </span>
            </Show>
          </div>
          <Show when={s()?.upstream}>
            <p class="text-[11px] text-background/65">
              Tracking: <span class="font-mono text-background font-medium">{s()?.upstream}</span>
              <Show when={!hasAheadBehind()}> (up to date)</Show>
            </p>
          </Show>
          <div class="space-y-0.5 text-[11px]">
            <Show when={(s()?.staged ?? 0) > 0}>
              <p class="text-[color:color-mix(in_srgb,var(--success)_65%,var(--background))] font-medium">● {s()?.staged} {s()?.staged === 1 ? "file" : "files"} staged</p>
            </Show>
            <Show when={(s()?.dirty ?? 0) > 0}>
              <p class="text-[color:color-mix(in_srgb,var(--warning)_65%,var(--background))] font-medium">! {s()?.dirty} {s()?.dirty === 1 ? "file" : "files"} modified (unstaged)</p>
            </Show>
            <Show when={(s()?.untracked ?? 0) > 0}>
              <p class="text-background/65">? {s()?.untracked} untracked {s()?.untracked === 1 ? "file" : "files"}</p>
            </Show>
            <Show when={(s()?.conflicts ?? 0) > 0}>
              <p class="text-[color:color-mix(in_srgb,var(--destructive)_65%,var(--background))] font-semibold">× {s()?.conflicts} conflicting {s()?.conflicts === 1 ? "file" : "files"}</p>
            </Show>
            <Show when={isClean()}>
              <p class="text-background/65">✓ Working tree clean</p>
            </Show>
          </div>
          <p class="border-t border-border/60 pt-1 text-[10px] text-background/55">
            Click to open Git history (g)
          </p>
        </TooltipContent>
      </Tooltip>
    </Show>
  );
}

function ProjectItem(props: { name: string }) {
  const project = createMemo(() => projectsList().find((p) => p.name === props.name));
  const serviceList = createMemo(() => servicesMap()[props.name]);
  const active = () => selectedProject() === props.name;
  const selected = () => active() && selectedService() === null && selectedTask() === null;
  const cursor = () => sameNavItem(keyboardCursor(), { kind: "project", project: props.name });

  const aggregateHealth = createMemo(() => {
    const runningWithHealth = (serviceList() ?? []).filter(
      (s) => (s.hasHealth ?? (s as any).has_health) && (s.status === "running" || s.status === "starting")
    );
    if (runningWithHealth.length === 0) return "";
    if (runningWithHealth.some((s) => s.health === "unhealthy")) return "unhealthy";
    if (runningWithHealth.some((s) => s.health === "starting" || s.health === "starting_healthy")) return "starting";
    if (runningWithHealth.every((s) => s.health === "healthy")) return "healthy";
    return "";
  });
  const taskList = createMemo(() => tasksMap()[props.name] ?? []);

  return (
    <Show when={project()}>
      {(p) => (
        <Collapsible
          open={isProjectExpanded(props.name)}
          onOpenChange={(open) => setProjectExpanded(props.name, open)}
        >
          <ContextMenu>
            <ContextMenuTrigger
              as="div"
              data-kbd-cursor={cursor() ? "" : undefined}
              class={cn(
                "group/proj relative flex h-8 items-center border-l-2 pr-2",
                active() ? "border-primary" : "border-transparent",
                selected()
                  ? "bg-accent text-accent-foreground"
                  : "hover:bg-muted/70",
                !selected() && cursor() && "bg-muted"
              )}
              onContextMenu={() => selectProject(props.name)}
            >
              <CollapsibleTrigger class="flex h-8 w-6 shrink-0 items-center justify-center text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring [&[data-expanded]_svg]:rotate-90">
                <ChevronRight class="size-3.5 transition-transform duration-100" />
                <span class="sr-only">Toggle</span>
              </CollapsibleTrigger>
              <button
                type="button"
                onClick={() => selectProject(props.name)}
                class="flex h-full min-w-0 flex-1 items-center gap-2 text-left text-[13px] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
              >
                <Boxes class="size-3.5 shrink-0 text-muted-foreground" />
                <span class="truncate font-medium">{props.name}</span>
              </button>

              <div class="ml-auto flex shrink-0 items-center gap-1.5 pl-1">
                {/* Hover actions — space is always reserved (opacity, not display) so the
                    git badge never shifts when the actions fade in on hover. */}
                <div
                  class={cn(
                    "flex items-center gap-0.5 transition-opacity duration-100",
                    "pointer-events-none opacity-0",
                    "group-hover/proj:pointer-events-auto group-hover/proj:opacity-100",
                    "focus-within:pointer-events-auto focus-within:opacity-100"
                  )}
                >
                  <Show
                    when={p().status !== "stopped"}
                    fallback={
                      <Tooltip>
                        <TooltipTrigger
                          as={Button}
                          variant="ghost"
                          size="icon-xs"
                          class="text-success hover:text-success hover:bg-success/15"
                          onClick={(e: MouseEvent) => {
                            e.stopPropagation();
                            startProject(props.name);
                          }}
                        >
                          <Play class="size-3" />
                          <span class="sr-only">Start project</span>
                        </TooltipTrigger>
                        <TooltipContent>Start project (u)</TooltipContent>
                      </Tooltip>
                    }
                  >
                    <Tooltip>
                      <TooltipTrigger
                        as={Button}
                        variant="ghost"
                        size="icon-xs"
                        class="text-muted-foreground hover:text-foreground"
                        onClick={(e: MouseEvent) => {
                          e.stopPropagation();
                          startProject(props.name);
                        }}
                      >
                        <RefreshCw class="size-3" />
                        <span class="sr-only">Reload config</span>
                      </TooltipTrigger>
                      <TooltipContent>Reload config & prune orphans</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        as={Button}
                        variant="ghost"
                        size="icon-xs"
                        class="text-muted-foreground hover:text-destructive hover:bg-destructive/15"
                        onClick={(e: MouseEvent) => {
                          e.stopPropagation();
                          stopProject(props.name);
                        }}
                      >
                        <Power class="size-3" />
                        <span class="sr-only">Stop project</span>
                      </TooltipTrigger>
                      <TooltipContent>Stop project (d d)</TooltipContent>
                    </Tooltip>
                  </Show>
                </div>

                {/* Git status prompt tokens */}
                <GitPromptBadge project={props.name} />

                {/* Aggregate health */}
                <Show when={aggregateHealth()}>
                  <Tooltip>
                    <TooltipTrigger as="span" class="flex items-center">
                      <span class={cn("size-2 rounded-full", healthDot(true, aggregateHealth()))} />
                    </TooltipTrigger>
                    <TooltipContent>health: {aggregateHealth()}</TooltipContent>
                  </Tooltip>
                </Show>

                {/* Project status dot */}
                <ProjectStatusDot status={p().status} />
              </div>
            </ContextMenuTrigger>
            <ContextMenuContent>
              <ContextMenuItem onSelect={() => selectProject(props.name)}>
                <Boxes />
                View Logs
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => openGitView(props.name)}>
                <GitBranch />
                Git History
                <ContextMenuShortcut>g</ContextMenuShortcut>
              </ContextMenuItem>
              <ContextMenuSeparator />
              <Show
                when={p().status !== "stopped"}
                fallback={
                  <ContextMenuItem onSelect={() => startProject(props.name)}>
                    <Play />
                    Start Project
                    <ContextMenuShortcut>u</ContextMenuShortcut>
                  </ContextMenuItem>
                }
              >
                <ContextMenuItem onSelect={() => startProject(props.name)}>
                  <RefreshCw />
                  Reload Config
                </ContextMenuItem>
                <ContextMenuItem
                  variant="destructive"
                  onSelect={() => stopProject(props.name)}
                >
                  <Power />
                  Stop Project
                  <ContextMenuShortcut>d d</ContextMenuShortcut>
                </ContextMenuItem>
              </Show>
              <ContextMenuSeparator />
              <ContextMenuItem
                onSelect={() => setProjectExpanded(props.name, !isProjectExpanded(props.name))}
              >
                <ChevronRight />
                {isProjectExpanded(props.name) ? "Collapse Project" : "Expand Project"}
              </ContextMenuItem>
              <ContextMenuSeparator />
              <ContextMenuItem onSelect={() => copyToClipboard(props.name, "project name")}>
                <Copy />
                Copy Name
              </ContextMenuItem>
              <Show when={p().configPath}>
                <ContextMenuItem onSelect={() => copyToClipboard(p().configPath, "config path")}>
                  <Copy />
                  Copy Config Path
                </ContextMenuItem>
              </Show>
            </ContextMenuContent>
          </ContextMenu>
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
              <Show when={taskList().length > 0}>
                <div class="my-1 border-t pt-1">
                  <For each={taskList()}>
                    {(a) => <TaskRow project={props.name} name={a.name} />}
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
          <span class="truncate text-[13px] font-semibold tracking-tight">devyard</span>
        </button>

        <div class="flex items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => openPortsDialog()}
            class="text-muted-foreground"
            title="Ports & URLs"
            aria-label="Ports & URLs"
          >
            <Globe class="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => setShowSettingsModal(true)}
            class="text-muted-foreground"
            title="Settings"
            aria-label="Settings"
          >
            <Settings class="size-3.5" />
          </Button>
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
            <span class="capitalize">{eventStatus()}</span>
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
      <ContextMenu>
        <ContextMenuTrigger
          as="nav"
          class="flex-1 overflow-y-auto overflow-x-hidden py-2 outline-none"
          data-sidebar-nav
          tabindex="-1"
        >
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
              <code class="rounded bg-muted px-1 font-mono text-foreground">devyard start</code>.
            </p>
          </Show>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onSelect={() => setShowAddProject(true)}>
            <Plus />
            Add Project…
          </ContextMenuItem>
          <ContextMenuItem onSelect={() => refreshAll()}>
            <RefreshCw />
            Refresh All
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onSelect={() => {
              for (const name of projectNames()) {
                setProjectExpanded(name, true);
              }
            }}
          >
            <ChevronsUpDown />
            Expand All Projects
          </ContextMenuItem>
          <ContextMenuItem
            onSelect={() => {
              for (const name of projectNames()) {
                setProjectExpanded(name, false);
              }
            }}
          >
            <ChevronsDownUp />
            Collapse All Projects
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onSelect={() => {
              fetchDaemonStatus();
              setShowDaemonModal(true);
            }}
          >
            <Boxes />
            Daemon Status…
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

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
