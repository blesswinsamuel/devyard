import { createEffect, createRoot, createSignal, untrack } from "solid-js";
import type { ProjectInfo, ServiceState, ViewMode, ShellTab, PaneNode } from "./types";
import { sendWS, onWS, onWSOpen, wsStatus, connectWS } from "./ws";

export type Theme = "dark" | "light";

const storedTheme = (localStorage.getItem("lc-theme") as Theme | null) ?? "dark";
const [theme, setTheme] = createSignal<Theme>(storedTheme);

createRoot(() => {
  createEffect(() => {
    const t = theme();
    document.documentElement.classList.toggle("dark", t === "dark");
    localStorage.setItem("lc-theme", t);
  });
});

export type NavItem =
  | { kind: "project"; project: string }
  | { kind: "service"; project: string; service: string };

const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
const [services, setServices] = createSignal<Record<string, ServiceState[]>>({});
/** Projects the user has collapsed; everything else is expanded by default. */
const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set());
const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
const [selectedService, setSelectedService] = createSignal<string | null>(null);
/** Active main view tab ("logs" | "shell" | "git" | "agents"). Defaults to "logs". */
const [activeView, setActiveView] = createSignal<ViewMode>("logs");
/** Keyboard focus in the sidebar (highlight); Enter commits to selection. */
const [keyboardCursor, setKeyboardCursor] = createSignal<NavItem | null>(null);
const [showHelp, setShowHelp] = createSignal(false);
const [toasts, setToasts] = createSignal<{ id: number; message: string; kind: "error" | "info" }[]>([]);

export function isProjectExpanded(name: string): boolean {
  return !collapsed().has(name);
}

export function sameNavItem(a: NavItem | null, b: NavItem | null): boolean {
  if (!a || !b) return a === b;
  if (a.kind !== b.kind || a.project !== b.project) return false;
  if (a.kind === "service" && b.kind === "service") return a.service === b.service;
  return true;
}

export function navItemKey(item: NavItem): string {
  return item.kind === "project" ? `p:${item.project}` : `s:${item.project}/${item.service}`;
}

/** Flat list of visible sidebar rows (projects + services of expanded projects). */
export function navItems(): NavItem[] {
  const items: NavItem[] = [];
  for (const p of projects()) {
    items.push({ kind: "project", project: p.name });
    if (!collapsed().has(p.name)) {
      for (const s of services()[p.name] ?? []) {
        items.push({ kind: "service", project: p.name, service: s.name });
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
  return { kind: "project", project: proj };
}

function pruneCursor() {
  const cur = untrack(keyboardCursor);
  if (!cur) return;
  const items = untrack(navItems);
  if (!items.some((it) => sameNavItem(it, cur))) {
    setKeyboardCursor(selectionAsNavItem());
  }
}

/** Ensure a cursor exists, seeded from selection or the first nav item. */
export function ensureKeyboardCursor(): NavItem | null {
  const cur = keyboardCursor();
  const items = navItems();
  if (items.length === 0) {
    setKeyboardCursor(null);
    return null;
  }
  if (cur && items.some((it) => sameNavItem(it, cur))) return cur;
  const seeded = selectionAsNavItem();
  if (seeded && items.some((it) => sameNavItem(it, seeded))) {
    setKeyboardCursor(seeded);
    return seeded;
  }
  setKeyboardCursor(items[0]);
  return items[0];
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
  setKeyboardCursor(items[next]);
}

export function moveKeyboardCursorToEnd(toEnd: boolean) {
  const items = navItems();
  if (items.length === 0) {
    setKeyboardCursor(null);
    return;
  }
  setKeyboardCursor(toEnd ? items[items.length - 1] : items[0]);
}

export function commitKeyboardCursor() {
  const cur = ensureKeyboardCursor();
  if (!cur) return;
  if (cur.kind === "project") selectProject(cur.project);
  else selectService(cur.project, cur.service);
}

/** Service targeted by action keys: cursor service, else selected service. */
export function actionService(): { project: string; service: string } | null {
  const cur = keyboardCursor();
  if (cur?.kind === "service") return { project: cur.project, service: cur.service };
  const proj = selectedProject();
  const svc = selectedService();
  if (proj && svc) return { project: proj, service: svc };
  return null;
}

/** Project targeted by project-level action keys. */
export function actionProject(): string | null {
  const cur = keyboardCursor();
  if (cur) return cur.project;
  return selectedProject();
}

export function toggleHelp() {
  setShowHelp((v) => !v);
}

export function closeHelp() {
  setShowHelp(false);
}

let toastId = 0;
export function pushToast(message: string, kind: "error" | "info" = "error") {
  const id = ++toastId;
  setToasts((t) => [...t, { id, message, kind }]);
  setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 5000);
}

function sameProject(a: ProjectInfo, b: ProjectInfo): boolean {
  return a.name === b.name && a.status === b.status && a.config_path === b.config_path;
}

function sameService(a: ServiceState, b: ServiceState): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exit_code === b.exit_code &&
    a.restarts === b.restarts &&
    a.started_at === b.started_at &&
    a.finished_at === b.finished_at &&
    a.has_health === b.has_health &&
    a.health === b.health
  );
}

function sameProjectList(a: ProjectInfo[], b: ProjectInfo[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (!sameProject(a[i], b[i])) return false;
  }
  return true;
}

function sameServiceList(a: ServiceState[], b: ServiceState[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (!sameService(a[i], b[i])) return false;
  }
  return true;
}

function pruneSelection() {
  const names = new Set(untrack(projects).map((p) => p.name));
  const sel = untrack(selectedProject);
  if (sel && !names.has(sel)) {
    setSelectedProject(null);
    setSelectedService(null);
    pruneCursor();
    return;
  }
  const svc = untrack(selectedService);
  if (sel && svc) {
    const list = untrack(services)[sel];
    if (list && !list.some((s) => s.name === svc)) {
      setSelectedService(null);
    }
  }
  pruneCursor();
}

function prune() {
  const names = new Set(projects().map((p) => p.name));
  setServices((m) => {
    let changed = false;
    const next: Record<string, ServiceState[]> = {};
    for (const [k, v] of Object.entries(m)) {
      if (names.has(k)) next[k] = v;
      else changed = true;
    }
    return changed ? next : m;
  });
  setCollapsed((prev) => {
    let changed = false;
    const next = new Set<string>();
    for (const name of prev) {
      if (names.has(name)) next.add(name);
      else changed = true;
    }
    return changed ? next : prev;
  });
  pruneSelection();
}

function refreshServicesForVisible() {
  const collapsedSet = untrack(collapsed);
  const needed = new Set<string>();
  for (const p of untrack(projects)) {
    if (!collapsedSet.has(p.name)) needed.add(p.name);
  }
  const sel = untrack(selectedProject);
  if (sel) needed.add(sel);
  for (const p of needed) {
    sendWS({ type: "list_services", project: p });
  }
}

function refreshAll() {
  sendWS({ type: "list_projects" });
  refreshServicesForVisible();
}

function isStaleProjectError(message: string): boolean {
  return /project .+ is not running/.test(message);
}

function start() {
  connectWS();
  onWS("projects", (resp) => {
    const next = resp.data as ProjectInfo[];
    const prevNames = new Set(untrack(projects).map((p) => p.name));
    setProjects((prev) => (sameProjectList(prev, next) ? prev : next));
    prune();
    // First paint (and newly appeared projects) need a services fetch —
    // refreshAll() often runs before projects arrive, when the list is empty.
    const collapsedSet = untrack(collapsed);
    for (const p of next) {
      if (!collapsedSet.has(p.name) && (!prevNames.has(p.name) || !untrack(services)[p.name])) {
        sendWS({ type: "list_services", project: p.name });
      }
    }
  });
  onWS("services", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = resp.data as ServiceState[];
    setServices((m) => {
      const prev = m[project];
      if (prev && sameServiceList(prev, next)) return m;
      return { ...m, [project]: next };
    });
    pruneSelection();
  });
  onWS("error", (resp) => {
    if (!resp.error) return;
    // Poll races: list_services for a project removed between ticks.
    if (isStaleProjectError(resp.error)) return;
    pushToast(resp.error);
  });
  onWS("result", (resp) => {
    if (resp.ok === false || resp.error) {
      pushToast(resp.error || "action failed");
    }
  });

  // Fetch immediately and again on every reconnect (pending queue covers first open).
  refreshAll();
  const stopOpen = onWSOpen(() => refreshAll());

  const poll = setInterval(refreshAll, 2000);

  return () => {
    clearInterval(poll);
    stopOpen();
  };
}

export function refreshServices(project: string) {
  sendWS({ type: "list_services", project });
}

export function setProjectExpanded(name: string, open: boolean) {
  setCollapsed((prev) => {
    const isCollapsed = prev.has(name);
    if (open && isCollapsed) {
      const next = new Set(prev);
      next.delete(name);
      return next;
    }
    if (!open && !isCollapsed) {
      const next = new Set(prev);
      next.add(name);
      return next;
    }
    return prev;
  });
  if (open) refreshServices(name);
}

export function expandProject(name: string) {
  setProjectExpanded(name, true);
}

export function selectProject(name: string) {
  setSelectedProject(name);
  setSelectedService(null);
  expandProject(name);
  setKeyboardCursor({ kind: "project", project: name });
}

export function selectService(project: string, service: string) {
  setSelectedProject(project);
  setSelectedService(service);
  setActiveView("logs");
  expandProject(project);
  setKeyboardCursor({ kind: "service", project, service });
}

/** ArrowRight: expand project under cursor (or step into first service). ArrowLeft: collapse, or jump to parent. */
export function navigateKeyboardHorizontal(dir: "left" | "right") {
  const cur = ensureKeyboardCursor();
  if (!cur) return;
  if (dir === "right") {
    if (cur.kind !== "project") return;
    if (!isProjectExpanded(cur.project)) {
      setProjectExpanded(cur.project, true);
      return;
    }
    const items = navItems();
    const idx = items.findIndex((it) => sameNavItem(it, cur));
    const next = items[idx + 1];
    if (next?.kind === "service" && next.project === cur.project) {
      setKeyboardCursor(next);
    }
    return;
  }
  // left
  if (cur.kind === "service") {
    setKeyboardCursor({ kind: "project", project: cur.project });
    return;
  }
  if (isProjectExpanded(cur.project)) {
    setProjectExpanded(cur.project, false);
  }
}

export function restartService(project: string, service: string) {
  sendWS({ type: "restart_service", project, service });
  refreshServices(project);
}

export function stopService(project: string, service: string) {
  sendWS({ type: "stop_service", project, service });
  refreshServices(project);
}

export function killService(project: string, service: string, signal = "SIGKILL") {
  sendWS({ type: "kill_service", project, service, signal });
  refreshServices(project);
}

export function stopProject(project: string) {
  sendWS({ type: "stop_project", project });
  sendWS({ type: "list_projects" });
  refreshServices(project);
}

export function startProject(project: string, configPath?: string) {
  const path = configPath || projects().find((p) => p.name === project)?.config_path;
  sendWS({ type: "start_project", project, config_path: path || "" });
  sendWS({ type: "list_projects" });
  refreshServices(project);
}

export function startProjectByPath(configPath: string, envFile?: string) {
  sendWS({ type: "start_project", config_path: configPath, env_file: envFile || "" });
  sendWS({ type: "list_projects" });
}

export interface ProjectShellState {
  tabs: ShellTab[];
  activeTabId: string;
}

const [shellWorkspaces, setShellWorkspaces] = createSignal<Record<string, ProjectShellState>>({});

let paneIdCounter = 0;
export function generatePaneId(): string {
  return `term-${Date.now()}-${++paneIdCounter}`;
}

export function getProjectShellState(project: string): ProjectShellState {
  const current = shellWorkspaces()[project];
  if (current && current.tabs.length > 0) return current;

  const firstPaneId = generatePaneId();
  const initialTab: ShellTab = {
    id: `tab-${Date.now()}`,
    title: "Shell 1",
    rootPane: { type: "terminal", id: firstPaneId },
  };

  const newState: ProjectShellState = {
    tabs: [initialTab],
    activeTabId: initialTab.id,
  };

  setShellWorkspaces((prev) => ({ ...prev, [project]: newState }));
  return newState;
}

export function addShellTab(project: string) {
  const state = getProjectShellState(project);
  const newPaneId = generatePaneId();
  const newTab: ShellTab = {
    id: `tab-${Date.now()}`,
    title: `Shell ${state.tabs.length + 1}`,
    rootPane: { type: "terminal", id: newPaneId },
  };

  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      tabs: [...state.tabs, newTab],
      activeTabId: newTab.id,
    },
  }));
}

export function selectShellTab(project: string, tabId: string) {
  const state = getProjectShellState(project);
  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: { ...state, activeTabId: tabId },
  }));
}

export function closeShellTab(project: string, tabId: string) {
  const state = getProjectShellState(project);
  const remaining = state.tabs.filter((t) => t.id !== tabId);

  if (remaining.length === 0) {
    const newPaneId = generatePaneId();
    const fallbackTab: ShellTab = {
      id: `tab-${Date.now()}`,
      title: "Shell 1",
      rootPane: { type: "terminal", id: newPaneId },
    };
    setShellWorkspaces((prev) => ({
      ...prev,
      [project]: { tabs: [fallbackTab], activeTabId: fallbackTab.id },
    }));
    return;
  }

  const nextActive =
    state.activeTabId === tabId ? remaining[remaining.length - 1].id : state.activeTabId;

  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: { tabs: remaining, activeTabId: nextActive },
  }));
}

function splitNode(
  node: PaneNode,
  targetId: string,
  direction: "horizontal" | "vertical",
  newPaneId: string
): PaneNode {
  if (node.type === "terminal") {
    if (node.id === targetId) {
      return {
        type: "split",
        id: `split-${Date.now()}-${Math.random()}`,
        direction,
        children: [node, { type: "terminal", id: newPaneId }],
      };
    }
    return node;
  }
  return {
    ...node,
    children: node.children.map((child) =>
      splitNode(child, targetId, direction, newPaneId)
    ),
  };
}

function removeNode(node: PaneNode, targetId: string): PaneNode | null {
  if (node.type === "terminal") {
    return node.id === targetId ? null : node;
  }
  const nextChildren = node.children
    .map((child) => removeNode(child, targetId))
    .filter((child): child is PaneNode => child !== null);

  if (nextChildren.length === 0) return null;
  if (nextChildren.length === 1) return nextChildren[0];
  return { ...node, children: nextChildren };
}

export function splitShellPane(
  project: string,
  targetPaneId: string,
  direction: "horizontal" | "vertical"
) {
  const state = getProjectShellState(project);
  const activeTab = state.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;

  const newPaneId = generatePaneId();
  const updatedRoot = splitNode(activeTab.rootPane, targetPaneId, direction, newPaneId);

  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      ...state,
      tabs: state.tabs.map((t) =>
        t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t
      ),
    },
  }));
}

export function closeShellPane(project: string, targetPaneId: string) {
  const state = getProjectShellState(project);
  const activeTab = state.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;

  const updatedRoot = removeNode(activeTab.rootPane, targetPaneId);
  if (!updatedRoot) {
    closeShellTab(project, activeTab.id);
    return;
  }

  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      ...state,
      tabs: state.tabs.map((t) =>
        t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t
      ),
    },
  }));
}

export {
  theme,
  setTheme,
  projects,
  services,
  selectedProject,
  selectedService,
  activeView,
  setActiveView,
  keyboardCursor,
  showHelp,
  wsStatus,
  start,
  toasts,
};

