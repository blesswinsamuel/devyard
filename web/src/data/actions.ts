import type { Component } from "solid-js";
import {
  ArrowDown,
  ArrowUp,
  CloudDownload,
  Command,
  Download,
  ExternalLink,
  GitBranch,
  GitCommitHorizontal,
  ListMinus,
  ListPlus,
  Hammer,
  House,
  Keyboard,
  LayoutDashboard,
  PanelBottom,
  PanelLeft,
  Pin,
  Play,
  Plug,
  Plus,
  Power,
  RefreshCw,
  RotateCw,
  ScrollText,
  Settings,
  Skull,
  Square,
  SquareTerminal,
  SunMoon,
  TextCursorInput,
  Trash2,
  Upload,
} from "lucide-solid";
import type { RouteTarget } from "~/lib/paths";
import { paths } from "~/lib/paths";
import { SERVICE_KILLABLE, SERVICE_STARTABLE, SERVICE_STOPPABLE, TASK_ACTIVE } from "~/lib/status";
import type { DaemonClient } from "./client";
import {
  entityKey,
  sortProjects,
  type DaemonEntity,
  type EntityState,
  type GitEntity,
  type ProjectEntity,
  type ServiceEntity,
  type TaskEntity,
} from "./entities";

/**
 * The single action registry. Palette, context menus, dropdowns, header
 * buttons and single-key shortcuts are all views over this list, so an
 * action's availability (`when`), confirmation and pending state are
 * defined exactly once.
 */

export type ActionTarget = RouteTarget;
export type ActionScope = ActionTarget["kind"];
export type ActionGroup = "service" | "task" | "project" | "git" | "navigation" | "view" | "daemon";

export interface ActionContext {
  target: ActionTarget;
  project?: ProjectEntity;
  service?: ServiceEntity;
  task?: TaskEntity;
  git?: GitEntity;
  /** Project ids in list order. */
  projectOrder: string[];
  daemon: DaemonEntity | null;
}

export interface ActionEnv {
  api: DaemonClient;
  navigate(path: string): void;
  currentPath(): string;
  openTerminal(project: string): void;
  attach(kind: "task" | "service", project: string, name: string): void;
  pinLogs(project: string, service?: string): void;
  promptTaskArgs(task: TaskEntity): Promise<string[] | null>;
  openAddProject(): void;
  openShortcuts(): void;
  openPalette(): void;
  /** Git page on the working tree with the commit message focused. */
  openGitCommit(project: string): void;
  toggleSidebar(): void;
  toggleDock(): void;
  cycleTheme(): void;
  notify(message: string, description?: string): void;
}

export interface ConfirmSpec {
  title: string;
  description: string;
  confirmLabel: string;
}

export interface Action {
  id: string;
  /** Short label for buttons and entity-scoped menus ("Restart"). */
  label: string;
  /** Self-describing title for the palette ("Restart service api"). */
  title?: (ctx: ActionContext) => string;
  icon?: Component<{ class?: string }>;
  shortcut?: string;
  group: ActionGroup;
  scope: ActionScope;
  /** Project actions that also apply from a service/task target (terminal, git). */
  inherit?: boolean;
  /** Pure UI action (no RPC): allowed while disconnected. */
  local?: boolean;
  destructive?: boolean;
  when: (ctx: ActionContext) => boolean;
  confirm?: (ctx: ActionContext) => ConfirmSpec;
  run: (ctx: ActionContext, env: ActionEnv) => unknown;
}

const always = () => true;
// Narrowing helpers: `when` guarantees the entity exists before `run`.
const svc = (ctx: ActionContext) => ctx.service!;
const task = (ctx: ActionContext) => ctx.task!;
const proj = (ctx: ActionContext) => ctx.project!;
/** A repository with no push/pull/fetch in flight (one runs at a time). */
const gitIdle = (c: ActionContext) => !!c.project && !!c.git?.isRepo && !c.git.syncOperation;
const gitOutput = (output: string) => output.trim() || undefined;

export const ACTIONS: Action[] = [
  // ---------------------------------------------------------------- service
  {
    id: "service.start",
    label: "Start",
    title: (c) => `Start service ${svc(c).name}`,
    icon: Play,
    shortcut: "s",
    group: "service",
    scope: "service",
    when: (c) => !!c.service && SERVICE_STARTABLE.includes(c.service.status),
    run: (c, env) => env.api.startService({ project: svc(c).project, service: svc(c).name }),
  },
  {
    id: "service.stop",
    label: "Stop",
    title: (c) => `Stop service ${svc(c).name}`,
    icon: Square,
    shortcut: "s",
    group: "service",
    scope: "service",
    when: (c) => !!c.service && SERVICE_STOPPABLE.includes(c.service.status),
    run: (c, env) => env.api.stopService({ project: svc(c).project, service: svc(c).name }),
  },
  {
    id: "service.restart",
    label: "Restart",
    title: (c) => `Restart service ${svc(c).name}`,
    icon: RotateCw,
    shortcut: "r",
    group: "service",
    scope: "service",
    when: (c) => !!c.service && SERVICE_STOPPABLE.includes(c.service.status),
    run: (c, env) => env.api.restartService({ project: svc(c).project, service: svc(c).name }),
  },
  {
    id: "service.rebuild",
    label: "Rebuild & restart",
    title: (c) => `Rebuild & restart service ${svc(c).name}`,
    icon: Hammer,
    group: "service",
    scope: "service",
    when: (c) => !!c.service?.spec.buildCommand && c.service.status !== "stopping" && c.service.status !== "building",
    run: (c, env) => {
      const s = svc(c);
      return SERVICE_STARTABLE.includes(s.status)
        ? env.api.startService({ project: s.project, service: s.name, build: true })
        : env.api.restartService({ project: s.project, service: s.name, build: true });
    },
  },
  {
    id: "service.kill",
    label: "Kill",
    title: (c) => `Kill service ${svc(c).name}`,
    icon: Skull,
    shortcut: "k",
    group: "service",
    scope: "service",
    destructive: true,
    when: (c) => !!c.service && SERVICE_KILLABLE.includes(c.service.status),
    confirm: (c) => ({
      title: `Kill ${svc(c).name}?`,
      description: `Sends SIGKILL to the whole process group of ${svc(c).name}. The process gets no chance to clean up; its restart policy still applies.`,
      confirmLabel: "Kill",
    }),
    run: (c, env) => env.api.killService({ project: svc(c).project, service: svc(c).name, signal: "SIGKILL" }),
  },
  {
    id: "service.logs",
    label: "Logs",
    title: (c) => `Logs of ${svc(c).name}`,
    icon: ScrollText,
    shortcut: "l",
    group: "service",
    scope: "service",
    local: true,
    when: (c) => !!c.service,
    run: (c, env) => env.navigate(paths.service(svc(c).project, svc(c).name, "logs")),
  },
  {
    id: "service.attach",
    label: "Attach",
    title: (c) => `Attach to ${svc(c).name} in the dock`,
    icon: Plug,
    shortcut: "a",
    group: "service",
    scope: "service",
    local: true,
    when: (c) => !!c.service?.spec.tty && c.service.status === "running",
    run: (c, env) => env.attach("service", svc(c).project, svc(c).name),
  },
  {
    id: "service.pin-logs",
    label: "Pin logs to dock",
    title: (c) => `Pin logs of ${svc(c).name} to the dock`,
    icon: Pin,
    group: "service",
    scope: "service",
    local: true,
    when: (c) => !!c.service,
    run: (c, env) => env.pinLogs(svc(c).project, svc(c).name),
  },
  {
    id: "service.open-url",
    label: "Open URL",
    title: (c) => `Open ${svc(c).urls[0]}`,
    icon: ExternalLink,
    shortcut: "o",
    group: "service",
    scope: "service",
    local: true,
    when: (c) => !!c.service?.urls.length,
    run: (c) => void window.open(svc(c).urls[0], "_blank", "noopener"),
  },

  // ------------------------------------------------------------------- task
  {
    id: "task.run",
    label: "Run",
    title: (c) => `Run task ${task(c).name}`,
    icon: Play,
    shortcut: "r",
    group: "task",
    scope: "task",
    when: (c) => !!c.task && !TASK_ACTIVE.includes(c.task.status),
    run: (c, env) => env.api.runTask({ project: task(c).project, task: task(c).name }),
  },
  {
    id: "task.run-args",
    label: "Run with args…",
    title: (c) => `Run task ${task(c).name} with arguments…`,
    icon: TextCursorInput,
    group: "task",
    scope: "task",
    when: (c) => !!c.task && !TASK_ACTIVE.includes(c.task.status),
    run: async (c, env) => {
      const args = await env.promptTaskArgs(task(c));
      if (args) await env.api.runTask({ project: task(c).project, task: task(c).name, args });
    },
  },
  {
    id: "task.stop",
    label: "Stop",
    title: (c) => `Stop task ${task(c).name}`,
    icon: Square,
    shortcut: "s",
    group: "task",
    scope: "task",
    when: (c) => c.task?.status === "running" || c.task?.status === "waiting",
    run: (c, env) => env.api.stopTask({ project: task(c).project, task: task(c).name }),
  },
  {
    id: "task.kill",
    label: "Kill",
    title: (c) => `Kill task ${task(c).name}`,
    icon: Skull,
    shortcut: "k",
    group: "task",
    scope: "task",
    destructive: true,
    when: (c) => c.task?.status === "running" || c.task?.status === "stopping",
    confirm: (c) => ({
      title: `Kill task ${task(c).name}?`,
      description: "Sends SIGKILL to the task's process group immediately.",
      confirmLabel: "Kill",
    }),
    run: (c, env) => env.api.killTask({ project: task(c).project, task: task(c).name, signal: "SIGKILL" }),
  },
  {
    id: "task.open",
    label: "Output",
    title: (c) => `Output of task ${task(c).name}`,
    icon: ScrollText,
    shortcut: "l",
    group: "task",
    scope: "task",
    local: true,
    when: (c) => !!c.task,
    run: (c, env) => env.navigate(paths.task(task(c).project, task(c).name)),
  },
  {
    id: "task.attach",
    label: "Open in dock",
    title: (c) => `Open task ${task(c).name} session in the dock`,
    icon: Plug,
    shortcut: "a",
    group: "task",
    scope: "task",
    local: true,
    when: (c) => c.task?.status === "running" && c.task.spec.tty,
    run: (c, env) => env.attach("task", task(c).project, task(c).name),
  },

  // ---------------------------------------------------------------- project
  {
    id: "project.start",
    label: "Start",
    title: (c) => `Start project ${proj(c).id}`,
    icon: Play,
    shortcut: "s",
    group: "project",
    scope: "project",
    when: (c) => !!c.project && !c.project.error && (c.project.status === "stopped" || c.project.desired === "partial"),
    run: (c, env) => env.api.startProject({ project: proj(c).id }),
  },
  {
    id: "project.stop",
    label: "Stop",
    title: (c) => `Stop project ${proj(c).id}`,
    icon: Square,
    shortcut: "s",
    group: "project",
    scope: "project",
    destructive: true,
    when: (c) => !!c.project && !["stopped", "stopping", "error"].includes(c.project.status),
    confirm: (c) => ({
      title: `Stop ${proj(c).id}?`,
      description: `Stops all ${proj(c).servicesRunning} running services and tasks. The project stays stopped (also across daemon restarts) until you start it again.`,
      confirmLabel: "Stop project",
    }),
    run: (c, env) => env.api.stopProject({ project: proj(c).id }),
  },
  {
    id: "project.restart",
    label: "Restart",
    title: (c) => `Restart project ${proj(c).id}`,
    icon: RotateCw,
    shortcut: "r",
    group: "project",
    scope: "project",
    when: (c) => !!c.project && ["running", "degraded", "starting"].includes(c.project.status),
    run: (c, env) => env.api.restartProject({ project: proj(c).id }),
  },
  {
    id: "project.reload",
    label: "Reload config",
    title: (c) => `Reload config of ${proj(c).id}`,
    icon: RefreshCw,
    group: "project",
    scope: "project",
    when: (c) => !!c.project,
    run: async (c, env) => {
      await env.api.reloadProject({ project: proj(c).id });
      env.notify(`Reloaded ${proj(c).id}`);
    },
  },
  {
    id: "project.rebuild",
    label: "Rebuild & start",
    title: (c) => `Rebuild & start project ${proj(c).id}`,
    icon: Hammer,
    group: "project",
    scope: "project",
    when: (c) => !!c.project && !c.project.error,
    run: (c, env) =>
      proj(c).status === "stopped"
        ? env.api.startProject({ project: proj(c).id, build: true })
        : env.api.restartProject({ project: proj(c).id, build: true }),
  },
  {
    id: "project.terminal",
    label: "Terminal",
    title: (c) => `Open a terminal in ${proj(c).id}`,
    icon: SquareTerminal,
    shortcut: "t",
    group: "project",
    scope: "project",
    inherit: true,
    local: true,
    when: (c) => !!c.project && !c.project.error,
    run: (c, env) => env.openTerminal(proj(c).id),
  },
  {
    id: "project.git",
    label: "Git",
    title: (c) => `Git of ${proj(c).id}`,
    icon: GitBranch,
    shortcut: "g",
    group: "project",
    scope: "project",
    inherit: true,
    local: true,
    when: (c) => !!c.project && c.git?.isRepo !== false,
    run: (c, env) => env.navigate(paths.git(proj(c).id)),
  },

  // -------------------------------------------------------------------- git
  {
    id: "git.fetch",
    label: "Fetch",
    title: (c) => `Git fetch in ${proj(c).id}`,
    icon: CloudDownload,
    group: "git",
    scope: "project",
    when: gitIdle,
    run: async (c, env) => {
      const res = await env.api.gitFetch({ project: proj(c).id });
      env.notify(`Fetched ${proj(c).id}`, gitOutput(res.output));
    },
  },
  {
    id: "git.pull",
    label: "Pull",
    title: (c) => `Git pull in ${proj(c).id}`,
    icon: Download,
    group: "git",
    scope: "project",
    when: gitIdle,
    run: async (c, env) => {
      const res = await env.api.gitPull({ project: proj(c).id });
      env.notify(`Pulled ${proj(c).id}`, gitOutput(res.output));
    },
  },
  {
    id: "git.push",
    label: "Push",
    title: (c) => `Git push in ${proj(c).id}`,
    icon: Upload,
    group: "git",
    scope: "project",
    when: gitIdle,
    run: async (c, env) => {
      const res = await env.api.gitPush({ project: proj(c).id });
      env.notify(`Pushed ${proj(c).id}`, gitOutput(res.output));
    },
  },
  {
    id: "git.stage-all",
    label: "Stage all",
    title: (c) => `Stage all changes in ${proj(c).id}`,
    icon: ListPlus,
    group: "git",
    scope: "project",
    when: (c) => !!c.project && !!c.git?.isRepo && c.git.dirty + c.git.untracked + c.git.conflicts > 0,
    run: (c, env) => env.api.gitStage({ project: proj(c).id, stageAll: true }),
  },
  {
    id: "git.unstage-all",
    label: "Unstage all",
    title: (c) => `Unstage all changes in ${proj(c).id}`,
    icon: ListMinus,
    group: "git",
    scope: "project",
    when: (c) => !!c.project && !!c.git?.isRepo && c.git.staged > 0,
    run: (c, env) => env.api.gitStage({ project: proj(c).id, stageAll: true, unstage: true }),
  },
  {
    id: "git.commit",
    label: "Commit…",
    title: (c) => `Commit staged changes in ${proj(c).id}…`,
    icon: GitCommitHorizontal,
    group: "git",
    scope: "project",
    local: true,
    when: (c) => !!c.project && !!c.git?.isRepo && c.git.staged > 0,
    run: (c, env) => env.openGitCommit(proj(c).id),
  },
  {
    id: "project.open",
    label: "Dashboard",
    title: (c) => `Open ${proj(c).id} dashboard`,
    icon: LayoutDashboard,
    group: "project",
    scope: "project",
    inherit: true,
    local: true,
    when: (c) => !!c.project,
    run: (c, env) => env.navigate(paths.project(proj(c).id)),
  },
  {
    id: "project.pin-logs",
    label: "Pin logs to dock",
    title: (c) => `Pin merged logs of ${proj(c).id} to the dock`,
    icon: Pin,
    group: "project",
    scope: "project",
    local: true,
    when: (c) => !!c.project,
    run: (c, env) => env.pinLogs(proj(c).id),
  },
  {
    id: "project.move-up",
    label: "Move up",
    title: (c) => `Move ${proj(c).id} up in the project list`,
    icon: ArrowUp,
    group: "project",
    scope: "project",
    when: (c) => !!c.project && c.projectOrder.indexOf(c.project.id) > 0,
    run: async (c, env) => {
      const id = proj(c).id;
      await env.api.moveProject({ project: id, index: c.projectOrder.indexOf(id) - 1 });
    },
  },
  {
    id: "project.move-down",
    label: "Move down",
    title: (c) => `Move ${proj(c).id} down in the project list`,
    icon: ArrowDown,
    group: "project",
    scope: "project",
    when: (c) => !!c.project && c.projectOrder.indexOf(c.project.id) >= 0 && c.projectOrder.indexOf(c.project.id) < c.projectOrder.length - 1,
    run: async (c, env) => {
      const id = proj(c).id;
      await env.api.moveProject({ project: id, index: c.projectOrder.indexOf(id) + 1 });
    },
  },
  {
    id: "project.remove",
    label: "Remove",
    title: (c) => `Remove project ${proj(c).id}`,
    icon: Trash2,
    group: "project",
    scope: "project",
    destructive: true,
    when: (c) => !!c.project,
    confirm: (c) => ({
      title: `Remove ${proj(c).id}?`,
      description: `Stops everything in ${proj(c).id}, removes it from the project list and deletes its logs and state. Your devyard.yml and source files are not touched. You can add it back from the Add project dialog.`,
      confirmLabel: "Remove project",
    }),
    run: async (c, env) => {
      const id = proj(c).id;
      await env.api.removeProject({ project: id });
      if (env.currentPath().startsWith(paths.project(id))) env.navigate(paths.home());
      env.notify(`Removed ${id}`);
    },
  },

  // -------------------------------------------------------------------- app
  {
    id: "app.palette",
    label: "Command palette",
    icon: Command,
    shortcut: "mod+k",
    group: "view",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.openPalette(),
  },
  {
    id: "app.shortcuts",
    label: "Keyboard shortcuts",
    icon: Keyboard,
    shortcut: "?",
    group: "view",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.openShortcuts(),
  },
  {
    id: "app.toggle-sidebar",
    label: "Toggle sidebar",
    icon: PanelLeft,
    shortcut: "mod+b",
    group: "view",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.toggleSidebar(),
  },
  {
    id: "app.toggle-dock",
    label: "Toggle dock",
    icon: PanelBottom,
    shortcut: "mod+j",
    group: "view",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.toggleDock(),
  },
  {
    id: "app.theme",
    label: "Cycle theme (light / dark / system)",
    icon: SunMoon,
    group: "view",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.cycleTheme(),
  },
  {
    id: "app.home",
    label: "Go to projects",
    icon: House,
    group: "navigation",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.navigate(paths.home()),
  },
  {
    id: "app.settings",
    label: "Settings",
    icon: Settings,
    shortcut: "mod+,",
    group: "navigation",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.navigate(paths.settings()),
  },
  {
    id: "app.add-project",
    label: "Add project…",
    icon: Plus,
    group: "project",
    scope: "app",
    local: true,
    when: always,
    run: (_c, env) => env.openAddProject(),
  },
  {
    id: "daemon.restart",
    label: "Restart daemon (keep services running)",
    icon: RefreshCw,
    group: "daemon",
    scope: "app",
    when: (c) => !!c.daemon && !c.daemon.draining,
    run: async (_c, env) => {
      await env.api.restartDaemon({ restartServices: false });
      env.notify("Daemon restarting…");
    },
  },
  {
    id: "daemon.restart-services",
    label: "Restart daemon and all services",
    icon: RotateCw,
    group: "daemon",
    scope: "app",
    destructive: true,
    when: (c) => !!c.daemon && !c.daemon.draining,
    confirm: () => ({
      title: "Restart daemon and services?",
      description: "Every running service and task in every project is stopped, the daemon restarts, and projects are started again.",
      confirmLabel: "Restart everything",
    }),
    run: async (_c, env) => {
      await env.api.restartDaemon({ restartServices: true });
      env.notify("Daemon restarting with services…");
    },
  },
  {
    id: "daemon.stop",
    label: "Stop daemon",
    icon: Power,
    group: "daemon",
    scope: "app",
    destructive: true,
    when: (c) => !!c.daemon && !c.daemon.draining,
    confirm: () => ({
      title: "Stop the devyard daemon?",
      description: "The dashboard disconnects until you run `devyard daemon start` (or any devyard command) again. Services keep running and are re-adopted by the next daemon.",
      confirmLabel: "Stop daemon",
    }),
    run: (_c, env) => env.api.stopDaemon({}),
  },
];

const actionById = new Map(ACTIONS.map((a) => [a.id, a] as const));

export function getAction(id: string): Action {
  const a = actionById.get(id);
  if (!a) throw new Error(`actions: unknown action ${id}`);
  return a;
}

export function resolveContext(target: ActionTarget, state: EntityState): ActionContext {
  const ctx: ActionContext = {
    target,
    daemon: state.daemon,
    projectOrder: sortProjects(Object.values(state.projects)).map((p) => p.id),
  };
  if (target.kind === "app") return ctx;
  ctx.project = state.projects[target.project];
  ctx.git = state.git[target.project];
  if (target.kind === "service") ctx.service = state.services[entityKey(target.project, target.name)];
  if (target.kind === "task") ctx.task = state.tasks[entityKey(target.project, target.name)];
  return ctx;
}

/** Whether `action` is offered for the context's target (ignores `when`). */
function inScope(action: Action, ctx: ActionContext, opts: { inherit?: boolean } = {}): boolean {
  if (action.scope === "app") return true;
  if (action.scope === ctx.target.kind) return true;
  return action.scope === "project" && !!ctx.project && (opts.inherit ?? false) && (action.inherit ?? false);
}

/** Actions applicable to the target, in registry order. */
export function actionsFor(
  ctx: ActionContext,
  opts: { inherit?: boolean; includeApp?: boolean } = {},
): Action[] {
  return ACTIONS.filter(
    (a) => (a.scope !== "app" || opts.includeApp) && inScope(a, ctx, opts) && a.when(ctx),
  );
}

/**
 * Resolves a key press to an action: target-scoped actions first (the same
 * key can mean start or stop depending on state), then inherited project
 * actions, then app actions.
 */
export function actionForShortcut(ctx: ActionContext, matches: (shortcut: string) => boolean): Action | undefined {
  const candidates = ACTIONS.filter((a) => a.shortcut && matches(a.shortcut));
  const tiers = [
    candidates.filter((a) => a.scope === ctx.target.kind && a.scope !== "app"),
    candidates.filter((a) => a.scope === "project" && a.inherit && ctx.target.kind !== "project" && !!ctx.project),
    candidates.filter((a) => a.scope === "app"),
  ];
  for (const tier of tiers) {
    const hit = tier.find((a) => a.when(ctx));
    if (hit) return hit;
  }
  return undefined;
}

export function actionTitle(action: Action, ctx: ActionContext): string {
  return action.title && action.scope !== "app" ? action.title(ctx) : action.label;
}

export function targetKey(t: ActionTarget): string {
  switch (t.kind) {
    case "app":
      return "app";
    case "project":
      return `project:${t.project}`;
    default:
      return `${t.kind}:${t.project}/${t.name}`;
  }
}
