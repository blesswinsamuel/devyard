import { batch, createEffect, createMemo, createSignal, untrack } from "solid-js";
import { connectWS } from "~/lib/ws";
import { onDaemonEvent, startEvents, stopEvents } from "~/lib/events";
import { listenPopState, parseRoute, pushRoute, replaceRoute, type RouteState } from "~/lib/router";
import {
  bindNavHooks,
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
import {
  gitTabId,
  openTab,
  overviewTabId,
  serviceLogTabId,
  taskLogTabId,
} from "~/stores/workspace";
import { setSidebarOpen } from "~/stores/app";
import { isMobile } from "~/lib/is-mobile";

// --- navigation model -------------------------------------------------------
//
// "Selection" is derived from the unified workspace: the focused pane's active
// tab decides what the header shows and which sidebar row is highlighted.
// Sidebar clicks open-or-focus the matching tab.

export type NavItem =
  | { kind: "project"; project: string }
  | { kind: "service"; project: string; service: string }
  | { kind: "task"; project: string; task: string };

const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
const [selectedService, setSelectedService] = createSignal<string | null>(null);
const [selectedTask, setSelectedTask] = createSignal<string | null>(null);
/** Projects the user has collapsed; everything else is expanded by default. */
const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set());
/**
 * Keyboard focus in the sidebar (visual highlight). Arrow keys move it
 * without committing; Enter commits it to the selection.
 */
const [keyboardCursor, setKeyboardCursor] = createSignal<NavItem | null>(null);

export { selectedProject, selectedService, selectedTask, keyboardCursor };

function applySelection(project: string | null, service: string | null, task: string | null) {
  batch(() => {
    setSelectedProject(project);
    setSelectedService(service);
    setSelectedTask(task);
  });
}

/** The workspace tab currently under the keyboard cursor, for actions. */
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

function pushProjectRoute(project: string | null) {
  pushRoute({ project, service: null, task: null });
}

function closeMobileSidebar() {
  // On phones the sidebar is a drawer; picking a target slides it away.
  if (isMobile()) setSidebarOpen(false);
}

export function selectProject(name: string, opts?: { skipPush?: boolean }) {
  batch(() => {
    applySelection(name, null, null);
    openTab({ kind: "overview", id: overviewTabId(name), project: name });
  });
  setProjectExpanded(name, true);
  setKeyboardCursor({ kind: "project", project: name });
  closeMobileSidebar();
  if (!opts?.skipPush) pushProjectRoute(name);
}

export function selectService(project: string, service: string, opts?: { skipPush?: boolean }) {
  batch(() => {
    applySelection(project, service, null);
    openTab({ kind: "log-service", id: serviceLogTabId(project, service), project, service });
  });
  setProjectExpanded(project, true);
  setKeyboardCursor({ kind: "service", project, service });
  closeMobileSidebar();
  if (!opts?.skipPush) pushProjectRoute(project);
}

export function selectTask(project: string, taskName: string, opts?: { skipPush?: boolean }) {
  batch(() => {
    applySelection(project, null, taskName);
    openTab({ kind: "log-task", id: taskLogTabId(project, taskName), project, task: taskName });
  });
  setProjectExpanded(project, true);
  setKeyboardCursor({ kind: "task", project, task: taskName });
  closeMobileSidebar();
  if (!opts?.skipPush) pushProjectRoute(project);
}

/** Opens (or focuses) the git tab for a project, optionally at a commit. */
export function openGitTab(name: string, commitHash?: string) {
  batch(() => {
    applySelection(name, null, null);
    openTab({ kind: "git", id: gitTabId(name), project: name });
  });
  setProjectExpanded(name, true);
  setKeyboardCursor({ kind: "project", project: name });
  closeMobileSidebar();
  loadGitLog(name);
  if (commitHash) {
    selectCommit(name, commitHash, { skipPush: true });
  }
  const commit = commitHash ?? untrack(selectedCommitHash)[name] ?? null;
  pushRoute({ project: name, service: null, task: null, view: "git", commit });
}

// --- pruning ----------------------------------------------------------------

function pruneSelection() {
  const projList = untrack(projectsData);
  const names = new Set(projList.map((p) => p.name));
  const sel = untrack(selectedProject);
  // Tab pruning (including the derived selection) happens in Main via
  // pruneWorkspace; here we only fix the sidebar cursor and URL.
  if (projList.length > 0 && sel && !names.has(sel)) {
    applySelection(null, null, null);
    pruneCursor();
    replaceRoute({ project: null, service: null, task: null });
    return;
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

  // Seed selection from the initial URL and open the matching tabs.
  const initRoute = parseRoute();
  if (initRoute.project) {
    if (initRoute.view === "git") {
      openGitTab(initRoute.project, initRoute.commit ?? undefined);
    } else if (initRoute.service) {
      selectService(initRoute.project, initRoute.service, { skipPush: true });
    } else if (initRoute.task) {
      selectTask(initRoute.project, initRoute.task, { skipPush: true });
    } else {
      selectProject(initRoute.project, { skipPush: true });
    }
  } else if (isMobile()) {
    // Nothing selected yet: lead phone users straight to the project list.
    setSidebarOpen(true);
  }

  const stopPopState = listenPopState((route: RouteState) => {
    if (!route.project) {
      applySelection(null, null, null);
      setKeyboardCursor(null);
      return;
    }
    if (route.view === "git") {
      openGitTab(route.project, route.commit ?? undefined);
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
