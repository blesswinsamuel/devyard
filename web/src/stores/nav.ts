import { batch, createEffect, createSignal, untrack } from "solid-js";
import { connectWS } from "~/lib/ws";
import { onDaemonEvent, startEvents, stopEvents } from "~/lib/events";
import { listenPopState, parseRoute, pushRoute, replaceRoute, type RouteState } from "~/lib/router";
import {
  clearSelectedCommit,
  initDataHandlers,
  loadGitLog,
  projects as projectsData,
  refreshAll,
  selectCommit,
  services as servicesData,
  selectedCommitHash,
  tasks as tasksData,
} from "~/stores/data";
import { ensureShellWorkspace } from "~/stores/shells";
import { setSidebarOpen } from "~/stores/app";
import { isMobile } from "~/lib/is-mobile";

// --- navigation model -------------------------------------------------------

export type NavItem =
  | { kind: "project"; project: string }
  | { kind: "service"; project: string; service: string }
  | { kind: "task"; project: string; task: string };

const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
const [selectedService, setSelectedService] = createSignal<string | null>(null);
const [selectedTask, setSelectedTask] = createSignal<string | null>(null);
/** Which main view is shown: logs or the full-page git view. */
const [activeView, setActiveView] = createSignal<"logs" | "git">("logs");
/** Projects the user has collapsed; everything else is expanded by default. */
const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set());
/**
 * Keyboard focus in the sidebar (visual highlight). Arrow keys move it
 * without committing; Enter commits it to the selection. Single highlight
 * system — no parallel ring.
 */
const [keyboardCursor, setKeyboardCursor] = createSignal<NavItem | null>(null);

export {
  selectedProject,
  selectedService,
  selectedTask,
  activeView,
  setActiveView,
  keyboardCursor,
};

export function sameNavItem(a: NavItem | null, b: NavItem | null): boolean {
  if (!a || !b) return a === b;
  if (a.kind !== b.kind || a.project !== b.project) return false;
  if (a.kind === "service" && b.kind === "service") return a.service === b.service;
  if (a.kind === "task" && b.kind === "task") return a.task === b.task;
  return true;
}

export function navItemKey(item: NavItem): string {
  if (item.kind === "project") return `p:${item.project}`;
  if (item.kind === "service") return `s:${item.project}/${item.service}`;
  return `t:${item.project}/${item.task}`;
}

export function isProjectExpanded(name: string): boolean {
  return !collapsed().has(name);
}

/** Flat list of visible sidebar rows (projects + children of expanded ones). */
export function navItems(): NavItem[] {
  const items: NavItem[] = [];
  for (const p of projectsData()) {
    items.push({ kind: "project", project: p.name });
    if (!collapsed().has(p.name)) {
      for (const s of servicesData()[p.name] ?? []) {
        items.push({ kind: "service", project: p.name, service: s.name });
      }
      for (const t of tasksData()[p.name] ?? []) {
        items.push({ kind: "task", project: p.name, task: t.name });
      }
    }
  }
  return items;
}

function selectionAsNavItem(): NavItem | null {
  const proj = selectedProject();
  if (!proj) return null;
  const svc = selectedService();
  if (svc) return { kind: "service", project: proj, service: svc };
  const task = selectedTask();
  if (task) return { kind: "task", project: proj, task: task };
  return { kind: "project", project: proj };
}

function pruneCursor() {
  const cur = untrack(keyboardCursor);
  if (!cur) return;
  if (!untrack(navItems).some((it) => sameNavItem(it, cur))) {
    setKeyboardCursor(selectionAsNavItem());
  }
}

export function ensureKeyboardCursor(): NavItem | null {
  const cur = keyboardCursor();
  const items = navItems();
  if (items.length === 0) {
    setKeyboardCursor(null);
    return null;
  }
  if (cur && items.some((it) => sameNavItem(it, cur))) return cur;
  const seeded = selectionAsNavItem();
  const seed = seeded && items.some((it) => sameNavItem(it, seeded)) ? seeded : items[0];
  setKeyboardCursor(seed ?? null);
  return seed ?? null;
}

export function moveKeyboardCursor(delta: number) {
  const items = navItems();
  if (items.length === 0) {
    setKeyboardCursor(null);
    return;
  }
  const cur = ensureKeyboardCursor();
  if (!cur) return;
  const idx = items.findIndex((it) => sameNavItem(it, cur));
  const next = Math.max(0, Math.min(items.length - 1, (idx < 0 ? 0 : idx) + delta));
  setKeyboardCursor(items[next]!);
}

export function moveKeyboardCursorToEnd(toEnd: boolean) {
  const items = navItems();
  if (items.length === 0) {
    setKeyboardCursor(null);
    return;
  }
  setKeyboardCursor(toEnd ? items[items.length - 1]! : items[0]!);
}

export function commitKeyboardCursor() {
  const cur = ensureKeyboardCursor();
  if (!cur) return;
  if (cur.kind === "project") selectProject(cur.project);
  else if (cur.kind === "service") selectService(cur.project, cur.service);
  else selectTask(cur.project, cur.task);
}

/** Service targeted by action keys: cursor service, else selected service. */
export function actionService(): { project: string; service: string } | null {
  const cur = keyboardCursor();
  if (cur?.kind === "service") return { project: cur.project, service: cur.service };
  const proj = selectedProject();
  const svc = selectedService();
  return proj && svc ? { project: proj, service: svc } : null;
}

/** Project targeted by project-level action keys. */
export function actionProject(): string | null {
  const cur = keyboardCursor();
  return cur ? cur.project : selectedProject();
}

/** ArrowRight expands / steps in; ArrowLeft steps out / collapses. */
export function navigateKeyboardHorizontal(dir: "left" | "right") {
  const cur = ensureKeyboardCursor();
  if (!cur) return;
  if (dir === "right") {
    if (cur.kind !== "project" || !isProjectExpanded(cur.project)) {
      if (cur.kind === "project") setProjectExpanded(cur.project, true);
      return;
    }
    const items = navItems();
    const next = items[items.findIndex((it) => sameNavItem(it, cur)) + 1];
    if (next?.kind === "service" && next.project === cur.project) {
      setKeyboardCursor(next);
    }
    return;
  }
  if (cur.kind !== "project") {
    setKeyboardCursor({ kind: "project", project: cur.project });
    return;
  }
  if (isProjectExpanded(cur.project)) {
    setProjectExpanded(cur.project, false);
  }
}

/** Target of the previous-logs toggle: cursor service/task, else selection. */
export function previousTarget(): NavItem | null {
  const cur = keyboardCursor();
  if (cur?.kind === "service" || cur?.kind === "task") return cur;
  const proj = selectedProject();
  const svc = selectedService();
  const task = selectedTask();
  if (proj && svc) return { kind: "service", project: proj, service: svc };
  if (proj && task) return { kind: "task", project: proj, task: task };
  return null;
}

// --- selection --------------------------------------------------------------

export function setProjectExpanded(name: string, open: boolean) {
  setCollapsed((prev) => {
    const isCollapsed = prev.has(name);
    if (open === isCollapsed) {
      const next = new Set(prev);
      if (open) next.delete(name);
      else next.add(name);
      return next;
    }
    return prev;
  });
}

function applySelection(
  project: string | null,
  service: string | null,
  task: string | null,
  view: "logs" | "git"
) {
  batch(() => {
    setSelectedProject(project);
    setSelectedService(service);
    setSelectedTask(task);
    setActiveView(view);
  });
  // On phones the sidebar is a drawer; picking a target slides it away.
  if (project && isMobile()) setSidebarOpen(false);
  // Shell workspaces are created eagerly so getters never mutate during render.
  if (project) ensureShellWorkspace(project);
}

export function selectProject(name: string, opts?: { skipPush?: boolean }) {
  applySelection(name, null, null, "logs");
  setProjectExpanded(name, true);
  setKeyboardCursor({ kind: "project", project: name });
  if (!opts?.skipPush) pushRoute({ project: name, service: null, task: null });
}

export function selectService(project: string, service: string, opts?: { skipPush?: boolean }) {
  applySelection(project, service, null, "logs");
  setProjectExpanded(project, true);
  setKeyboardCursor({ kind: "service", project, service });
  if (!opts?.skipPush) pushRoute({ project, service, task: null });
}

export function selectTask(project: string, taskName: string, opts?: { skipPush?: boolean }) {
  applySelection(project, null, taskName, "logs");
  setProjectExpanded(project, true);
  setKeyboardCursor({ kind: "task", project, task: taskName });
  if (!opts?.skipPush) pushRoute({ project, service: null, task: taskName });
}

/** Open the full-page git view for a project. */
export function openGitView(name: string, commitHash?: string) {
  applySelection(name, null, null, "git");
  setKeyboardCursor({ kind: "project", project: name });
  loadGitLog(name);
  if (commitHash) {
    selectCommit(name, commitHash, { skipPush: true });
  }
  const commit = commitHash ?? untrack(selectedCommitHash)[name] ?? null;
  pushRoute({ project: name, service: null, task: null, view: "git", commit });
}

/** Close the git view, returning to the project's log view. */
export function closeGitView() {
  const p = selectedProject();
  applySelection(p, p ? untrack(selectedService) : null, p ? untrack(selectedTask) : null, "logs");
  if (p) {
    pushRoute({ project: p, service: selectedService(), task: selectedTask(), view: "logs" });
  } else {
    pushRoute({ project: null, service: null, task: null });
  }
}

// --- pruning ----------------------------------------------------------------

function pruneSelection() {
  const projList = untrack(projectsData);
  const names = new Set(projList.map((p) => p.name));
  const sel = untrack(selectedProject);
  if (projList.length > 0 && sel && !names.has(sel)) {
    applySelection(null, null, null, "logs");
    pruneCursor();
    replaceRoute({ project: null, service: null, task: null });
    return;
  }
  if (sel) {
    const svc = untrack(selectedService);
    const svcList = untrack(servicesData)[sel];
    if (svc && svcList !== undefined && !svcList.some((s) => s.name === svc)) {
      applySelection(sel, null, null, untrack(activeView));
      replaceRoute({ project: sel, service: null, task: null });
    }
    const task = untrack(selectedTask);
    if (!task) return;
    const taskList = untrack(tasksData)[sel];
    if (taskList !== undefined && !taskList.some((a) => a.name === task)) {
      applySelection(sel, null, null, untrack(activeView));
      replaceRoute({ project: sel, service: null, task: null });
    }
  }
  pruneCursor();
}

// --- bootstrap --------------------------------------------------------------

let started = false;

/** Wires ConnectRPC event stream, route sync, and initial fetches. Call once. */
export function start(): () => void {
  if (started) return () => {};
  started = true;

  bindNavHooks({
    selectCommitRoute: (project, hash) => {
      selectCommit(project, hash);
    },
  });

  startEvents();
  connectWS();

  // Seed selection from the initial URL.
  const initRoute = parseRoute();
  if (initRoute.project) {
    applySelection(
      initRoute.project,
      initRoute.service ?? null,
      initRoute.task ?? null,
      initRoute.view === "git" ? "git" : "logs"
    );
    setProjectExpanded(initRoute.project, true);
    if (initRoute.view === "git") {
      loadGitLog(initRoute.project);
      if (initRoute.commit) {
        selectCommit(initRoute.project, initRoute.commit, { skipPush: true });
      }
    }
    const { service, task, project } = initRoute;
    setKeyboardCursor(
      service
        ? { kind: "service", project, service }
        : task
          ? { kind: "task", project, task }
          : { kind: "project", project: project! }
    );
  } else if (isMobile()) {
    // Nothing selected yet: lead phone users straight to the project list.
    setSidebarOpen(true);
  }

  const stopPopState = listenPopState((route: RouteState) => {
    if (!route.project) {
      applySelection(null, null, null, "logs");
      setKeyboardCursor(null);
      return;
    }
    if (route.view === "git") {
      applySelection(route.project, null, null, "git");
      loadGitLog(route.project);
      if (route.commit) {
        selectCommit(route.project, route.commit, { skipPush: true });
      } else {
        clearSelectedCommit(route.project);
      }
    } else if (route.service) {
      selectService(route.project, route.service, { skipPush: true });
    } else if (route.task) {
      selectTask(route.project, route.task, { skipPush: true });
    } else {
      selectProject(route.project, { skipPush: true });
    }
  });

  initDataHandlers();

  createEffect(() => {
    projectsData();
    servicesData();
    tasksData();
    pruneSelection();
  });

  const stopDaemonEvent = onDaemonEvent(() => {
    pruneSelection();
  });

  refreshAll();

  return () => {
    stopDaemonEvent();
    stopPopState();
    stopEvents();
  };
}
