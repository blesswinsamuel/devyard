import { createEffect, createMemo, createSignal, For, Match, Show, Switch } from "solid-js";
import { A, useLocation, useNavigate } from "@solidjs/router";
import { toast } from "solid-sonner";
import { ChevronRight, Command, FileDiff, PanelLeftClose, Play, Plus, Search, Settings } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { InputGroup, InputGroupAddon, InputGroupInput } from "~/components/ui/input-group";
import { ActionContextMenu } from "~/components/actions";
import { Logo } from "~/components/logo";
import { HealthIndicator, StatusDot } from "~/components/status";
import { Shortcut } from "~/components/shortcut";
import { entities, getGit, getProject, getService, getTask, projectList, servicesOf, tasksOf } from "~/data/entities";
import { api } from "~/data/client";
import { errorInfo } from "~/data/errors";
import { connection } from "~/data/sync";
import { createPersistedSignal } from "~/lib/persistence";
import { paths, targetFromPath, type RouteTarget } from "~/lib/paths";
import { isServiceFailing, isTransitional, projectTone, serviceLabel, serviceTone, taskLabel, taskTone } from "~/lib/status";
import { cn } from "~/lib/utils";
import { targetAttrs } from "~/app/runtime";
import { setAddProjectOpen, setPaletteOpen, setSidebarCollapsed } from "~/app/ui-state";
import { buildTree, dropIndex, gitSummary, nodeId, treeKey, type TreeNode } from "./tree";

const [expandedMap, setExpandedMap] = createPersistedSignal<Record<string, boolean>>("sidebar.expanded", {}, (raw) =>
  raw && typeof raw === "object" ? (raw as Record<string, boolean>) : undefined,
);

function nodeTarget(n: TreeNode): RouteTarget {
  if (n.kind === "project") return { kind: "project", project: n.project };
  return { kind: n.kind, project: n.project, name: n.name! };
}

function nodePath(n: TreeNode): string {
  if (n.kind === "project") return paths.project(n.project);
  if (n.kind === "service") return paths.service(n.project, n.name!);
  return paths.task(n.project, n.name!);
}

/** Restarts since the last explicit start: muted, amber from 5 (a likely
 * crash loop). Renders nothing at 0. */
function RestartBadge(props: { count: number; title: string }) {
  return (
    <Show when={props.count > 0}>
      <span
        class={cn("tabular shrink-0 text-2xs", props.count >= 5 ? "text-warning" : "text-muted-foreground")}
        title={props.title}
        aria-label={props.title}
      >
        ↻{props.count}
      </span>
    </Show>
  );
}

function ProjectRow(props: { node: TreeNode; onToggle: () => void }) {
  const p = () => getProject(props.node.project);
  const failing = createMemo(() => servicesOf(props.node.project).filter(isServiceFailing).length);
  const restarts = createMemo(() => servicesOf(props.node.project).reduce((n, s) => n + s.restarts, 0));
  const git = () => getGit(props.node.project);
  return (
    <Show when={p()}>
      {(project) => (
        <>
          <span
            class="-ml-1 flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-sidebar-accent"
            aria-hidden="true"
            onClick={(e) => {
              e.stopPropagation();
              props.onToggle();
            }}
          >
            <ChevronRight class={cn("size-3.5 transition-transform", props.node.expanded && "rotate-90")} />
          </span>
          <StatusDot tone={projectTone(project())} pulse={isTransitional(project().status)} />
          <span class="min-w-0 flex-1 truncate font-medium">{project().id}</span>
          <Show when={gitSummary(git())}>
            {(summary) => (
              <span class="flex max-w-24 min-w-0 items-center gap-1 text-2xs text-muted-foreground" title={`git: ${summary()}`}>
                <span class="truncate">{summary()}</span>
                <Show when={git() && !git()!.isClean}>
                  <span class="size-1.5 shrink-0 rounded-full bg-warning" aria-label="uncommitted changes" />
                </Show>
              </span>
            )}
          </Show>
          <Show when={project().drift}>
            {(d) => (
              <span
                class={cn("shrink-0", d().state === "invalid" ? "text-destructive" : "text-warning")}
                title={d().state === "invalid" ? "Config changed but does not load" : "Config changed on disk: not applied yet"}
                role="img"
                aria-label={d().state === "invalid" ? "config changed but does not load" : "config changed"}
              >
                <FileDiff class="size-3" />
              </span>
            )}
          </Show>
          <RestartBadge count={restarts()} title={`${restarts()} service ${restarts() === 1 ? "restart" : "restarts"}`} />
          <Show
            when={failing() > 0}
            fallback={
              <Show when={project().servicesTotal > 0}>
                <span class="tabular shrink-0 text-2xs text-muted-foreground" aria-label={`${project().servicesRunning} of ${project().servicesTotal} running`}>
                  {project().servicesRunning}/{project().servicesTotal}
                </span>
              </Show>
            }
          >
            <span class="tabular shrink-0 rounded-full bg-destructive/15 px-1.5 text-2xs font-medium text-destructive" aria-label={`${failing()} failing`}>
              {failing()}
            </span>
          </Show>
        </>
      )}
    </Show>
  );
}

function ServiceRow(props: { node: TreeNode }) {
  const s = () => getService(props.node.project, props.node.name!);
  return (
    <Show when={s()}>
      {(svc) => (
        <>
          <StatusDot tone={serviceTone(svc())} pulse={isTransitional(svc().status)} />
          <span class="min-w-0 flex-1 truncate">{svc().name}</span>
          <HealthIndicator health={svc().health} detail={svc().healthDetail} />
          <RestartBadge
            count={svc().restarts}
            title={`${svc().restarts} ${svc().restarts === 1 ? "restart" : "restarts"} since start${svc().finishedAt ? `, last exit ${svc().exitCode}` : ""}`}
          />
          <Show when={svc().status !== "running"}>
            <span class="shrink-0 text-2xs text-muted-foreground">{serviceLabel(svc())}</span>
          </Show>
        </>
      )}
    </Show>
  );
}

function TaskRow(props: { node: TreeNode }) {
  const t = () => getTask(props.node.project, props.node.name!);
  return (
    <Show when={t()}>
      {(task) => (
        <>
          <Play class="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate">{task().name}</span>
          <Show when={task().status !== "idle"}>
            <span class={cn("flex shrink-0 items-center gap-1 text-2xs", taskTone(task()) === "danger" ? "text-destructive" : "text-muted-foreground")}>
              <Show when={task().status === "running"}>
                <StatusDot tone="info" pulse />
              </Show>
              {taskLabel(task())}
            </span>
          </Show>
        </>
      )}
    </Show>
  );
}

/** Sidebar content (used inline on desktop and inside a sheet on mobile). */
export function Sidebar(props: { onNavigate?: () => void; collapsible?: boolean }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [filter, setFilter] = createSignal("");
  const [focused, setFocused] = createSignal<string>();
  let treeEl!: HTMLDivElement;

  const isExpanded = (project: string) => expandedMap()[project] ?? true;
  const setExpanded = (project: string, value: boolean) => setExpandedMap((m) => ({ ...m, [project]: value }));

  // Reuse unchanged node objects so <For> keeps rows (and focus) stable.
  let cache = new Map<string, TreeNode>();
  const nodes = createMemo(() => {
    const next = new Map<string, TreeNode>();
    const list = buildTree({ projects: projectList(), servicesOf, tasksOf, expanded: isExpanded, filter: filter() }).map(
      (n) => {
        const prev = cache.get(n.id);
        const keep =
          prev && prev.expanded === n.expanded && prev.posinset === n.posinset && prev.setsize === n.setsize;
        const node = keep ? prev : n;
        next.set(n.id, node);
        return node;
      },
    );
    cache = next;
    return list;
  });

  const selectedId = createMemo(() => {
    const t = targetFromPath(location.pathname);
    if (t.kind === "app") return undefined;
    return t.kind === "project" ? nodeId("project", t.project) : nodeId(t.kind, t.project, t.name);
  });

  // Keep a valid roving-tabindex item.
  createEffect(() => {
    const list = nodes();
    const f = focused();
    if (!f || !list.some((n) => n.id === f)) setFocused(selectedId() && list.some((n) => n.id === selectedId()) ? selectedId() : list[0]?.id);
  });

  const focusNode = (id: string | undefined) => {
    if (!id) return;
    setFocused(id);
    treeEl.querySelector<HTMLElement>(`[data-node-id="${CSS.escape(id)}"]`)?.focus();
  };

  const activate = (n: TreeNode) => {
    navigate(nodePath(n));
    props.onNavigate?.();
  };

  const onTreeKey = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const res = treeKey(nodes(), focused(), e.key);
    if (!res) return;
    e.preventDefault();
    e.stopPropagation();
    if (res.toggle) setExpanded(res.toggle.project, res.toggle.expanded);
    if (res.activate) activate(res.activate);
    queueMicrotask(() => focusNode(res.focus));
  };

  const phase = () => connection.state.phase;

  // Drag a project row to reorder the project list (not while filtering:
  // the visible rows are then not the list).
  const [dragging, setDragging] = createSignal<string>();
  const [drop, setDrop] = createSignal<{ project: string; after: boolean }>();
  const reorderable = () => !filter().trim() && projectList().length > 1;
  const endDrag = () => {
    setDragging(undefined);
    setDrop(undefined);
  };
  const dragProps = (node: TreeNode): Record<string, unknown> => ({
    draggable: true,
    onDragStart: (e: DragEvent) => {
      e.dataTransfer?.setData("text/plain", node.project);
      if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
      setDragging(node.project);
    },
    onDragEnd: endDrag,
    onDragOver: (e: DragEvent) => {
      const from = dragging();
      if (!from) return;
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
      const after = e.clientY > rect.top + rect.height / 2;
      if (dropIndex(projectList().map((p) => p.id), from, node.project, after) === null) {
        setDrop(undefined);
        return;
      }
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
      setDrop({ project: node.project, after });
    },
    onDrop: async (e: DragEvent) => {
      e.preventDefault();
      const from = dragging();
      const target = drop();
      endDrag();
      if (!from || !target) return;
      const index = dropIndex(projectList().map((p) => p.id), from, target.project, target.after);
      if (index === null) return;
      try {
        await api.moveProject({ project: from, index });
      } catch (err) {
        const info = errorInfo(err);
        toast.error(info.message ? `${info.reason}: ${info.message}` : info.reason);
      }
    },
  });
  const dropClass = (node: TreeNode) => {
    if (node.kind !== "project") return undefined;
    const d = drop();
    if (d?.project === node.project) return d.after ? "shadow-[inset_0_-2px_0_0_var(--ring)]" : "shadow-[inset_0_2px_0_0_var(--ring)]";
    return dragging() === node.project ? "opacity-50" : undefined;
  };

  return (
    <div class="flex h-full min-h-0 flex-col bg-sidebar text-sidebar-foreground">
      <div class="flex h-(--header-h) shrink-0 items-center gap-2 border-b border-sidebar-border px-3">
        <A href={paths.home()} class="focus-ring flex items-center gap-2 rounded-md font-semibold" onClick={() => props.onNavigate?.()}>
          <Logo />
          devyard
        </A>
        <Show when={props.collapsible}>
          <Button
            variant="ghost"
            size="icon-xs"
            class="ml-auto text-muted-foreground"
            aria-label="Collapse sidebar"
            title="Collapse sidebar (⌘B)"
            onClick={() => setSidebarCollapsed(true)}
          >
            <PanelLeftClose />
          </Button>
        </Show>
      </div>

      <div class="shrink-0 p-2">
        <InputGroup class="h-7">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            placeholder="Filter projects & services"
            aria-label="Filter projects and services"
            value={filter()}
            onInput={(e) => setFilter(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                focusNode(nodes()[0]?.id);
              } else if (e.key === "Escape") setFilter("");
            }}
          />
        </InputGroup>
      </div>

      <div
        ref={treeEl}
        class="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2"
        role="tree"
        aria-label="Projects"
        onKeyDown={onTreeKey}
      >
        <Show
          when={nodes().length > 0}
          fallback={
            <p class="px-2 py-6 text-center text-2xs text-muted-foreground">
              <Switch>
                <Match when={!entities.state.loaded}>Connecting…</Match>
                <Match when={filter()}>No matches.</Match>
                <Match when={true}>No projects yet.</Match>
              </Switch>
            </p>
          }
        >
          <For each={nodes()}>
            {(node) => (
              <ActionContextMenu
                target={nodeTarget(node)}
                class={cn(
                  "focus-ring group flex h-(--row-h) cursor-pointer items-center gap-2 rounded-md pr-2 text-ui select-none",
                  node.level === 1 ? "pl-2" : "pl-8",
                  selectedId() === node.id
                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                    : "hover:bg-sidebar-accent/50",
                  dropClass(node),
                )}
                triggerProps={{
                  role: "treeitem",
                  tabindex: focused() === node.id ? 0 : -1,
                  "aria-level": node.level,
                  "aria-posinset": node.posinset,
                  "aria-setsize": node.setsize,
                  "aria-expanded": node.kind === "project" ? !!node.expanded : undefined,
                  "aria-selected": selectedId() === node.id,
                  "aria-label": node.name ? `${node.kind} ${node.name}` : `project ${node.project}`,
                  "data-node-id": node.id,
                  ...targetAttrs(nodeTarget(node)),
                  onClick: () => {
                    setFocused(node.id);
                    activate(node);
                  },
                  onFocus: () => setFocused(node.id),
                  ...(node.kind === "project" && reorderable() ? dragProps(node) : {}),
                }}
              >
                <Switch>
                  <Match when={node.kind === "project"}>
                    <ProjectRow node={node} onToggle={() => setExpanded(node.project, !node.expanded)} />
                  </Match>
                  <Match when={node.kind === "service"}>
                    <ServiceRow node={node} />
                  </Match>
                  <Match when={node.kind === "task"}>
                    <TaskRow node={node} />
                  </Match>
                </Switch>
              </ActionContextMenu>
            )}
          </For>
        </Show>
      </div>

      <div class="flex shrink-0 flex-col gap-0.5 border-t border-sidebar-border p-1.5">
        <Button variant="ghost" size="sm" class="justify-start" onClick={() => setAddProjectOpen(true)}>
          <Plus /> Add project
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="justify-start"
          onClick={() => {
            setPaletteOpen(true);
            props.onNavigate?.();
          }}
        >
          <Command /> Command palette
          <Shortcut keys="mod+k" class="ml-auto" />
        </Button>
        <Button
          as={A}
          href={paths.settings()}
          variant="ghost"
          size="sm"
          class={cn("justify-start", location.pathname === paths.settings() && "bg-sidebar-accent")}
          onClick={() => props.onNavigate?.()}
        >
          <Settings /> Settings
          <span class="ml-auto flex items-center gap-1.5 text-2xs text-muted-foreground" title={connection.state.lastError || undefined}>
            <span
              class={cn(
                "size-1.5 rounded-full",
                phase() === "live" ? "bg-success" : phase() === "connecting" ? "bg-warning" : "bg-destructive",
              )}
            />
            {phase() === "live" ? entities.state.daemon?.version || "live" : phase()}
          </span>
        </Button>
      </div>
    </div>
  );
}
