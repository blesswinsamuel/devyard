import { createEffect, createRoot, createSignal, untrack } from "solid-js";
import type { ProjectInfo, ServiceState, ActionInfo, ActionState, GitCommit, GitBranch, GitTag, GitStash, GitLogPayload, ViewMode, ShellTab, PaneNode } from "./types";
import { sendWS, onWS, onWSOpen, wsStatus, connectWS, closeTerminal } from "./ws";
import { parseRoute, pushRoute, replaceRoute, listenPopState } from "./router";

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
  | { kind: "service"; project: string; service: string }
  | { kind: "action"; project: string; action: string };

const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
const [services, setServices] = createSignal<Record<string, ServiceState[]>>({});
const [actions, setActions] = createSignal<Record<string, ActionInfo[]>>({});
const [actionStates, setActionStates] = createSignal<Record<string, ActionState[]>>({});
const [gitCommits, setGitCommits] = createSignal<Record<string, GitCommit[]>>({});
const [gitBranches, setGitBranches] = createSignal<Record<string, GitBranch[]>>({});
const [gitTags, setGitTags] = createSignal<Record<string, GitTag[]>>({});
const [gitStashes, setGitStashes] = createSignal<Record<string, GitStash[]>>({});
const [gitError, setGitError] = createSignal<Record<string, string>>({});
const [gitLoading, setGitLoading] = createSignal<Record<string, boolean>>({});
const [selectedCommitHash, setSelectedCommitHash] = createSignal<Record<string, string | null>>({});
const [selectedFilePath, setSelectedFilePath] = createSignal<Record<string, string | null>>({});
const [gitDiffs, setGitDiffs] = createSignal<Record<string, Record<string, GitDiffResult>>>({});
const [gitDiffLoading, setGitDiffLoading] = createSignal<Record<string, boolean>>({});
const [gitDiffError, setGitDiffError] = createSignal<Record<string, string>>({});
const [gitCommitLoading, setGitCommitLoading] = createSignal<Record<string, boolean>>({});
const [gitCommitError, setGitCommitError] = createSignal<Record<string, string>>({});
export {
  actions,
  actionStates,
  gitCommits,
  gitBranches,
  gitTags,
  gitStashes,
  gitError,
  gitLoading,
  selectedCommitHash,
  selectedFilePath,
  gitDiffs,
  gitDiffLoading,
  gitDiffError,
  gitCommitLoading,
  gitCommitError,
};
/** Projects the user has collapsed; everything else is expanded by default. */
const [collapsed, setCollapsed] = createSignal<Set<string>>(new Set());
const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
const [selectedService, setSelectedService] = createSignal<string | null>(null);
const [selectedAction, setSelectedAction] = createSignal<string | null>(null);
/**
 * Targets whose log pane is showing the previous run instead of the live one,
 * keyed by navItemKey. Non-live (previous) panes never follow or auto-reset.
 */
const [previousLogs, setPreviousLogs] = createSignal<Set<string>>(new Set());
/** Active main view tab ("logs" | "shell" | "git"). Defaults to "logs". */
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
  if (a.kind === "action" && b.kind === "action") return a.action === b.action;
  return true;
}

export function navItemKey(item: NavItem): string {
  if (item.kind === "project") return `p:${item.project}`;
  if (item.kind === "service") return `s:${item.project}/${item.service}`;
  return `a:${item.project}/${item.action}`;
}

/** Flat list of visible sidebar rows (projects + services + actions of expanded projects). */
export function navItems(): NavItem[] {
  const items: NavItem[] = [];
  for (const p of projects()) {
    items.push({ kind: "project", project: p.name });
    if (!collapsed().has(p.name)) {
      for (const s of services()[p.name] ?? []) {
        items.push({ kind: "service", project: p.name, service: s.name });
      }
      for (const a of actions()[p.name] ?? []) {
        items.push({ kind: "action", project: p.name, action: a.name });
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
  const act = selectedAction();
  if (act) return { kind: "action", project: proj, action: act };
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
  else if (cur.kind === "service") selectService(cur.project, cur.service);
  else if (cur.kind === "action") selectAction(cur.project, cur.action);
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
  const projList = untrack(projects);
  const names = new Set(projList.map((p) => p.name));
  const sel = untrack(selectedProject);
  if (projList.length > 0 && sel && !names.has(sel)) {
    setSelectedProject(null);
    setSelectedService(null);
    setSelectedAction(null);
    pruneCursor();
    replaceRoute({ project: null, service: null, action: null });
    return;
  }
  const svc = untrack(selectedService);
  if (sel && svc) {
    const list = untrack(services)[sel];
    if (list && !list.some((s) => s.name === svc)) {
      setSelectedService(null);
      replaceRoute({ project: sel, service: null, action: null });
    }
  }
  const act = untrack(selectedAction);
  if (sel && act) {
    const list = untrack(actions)[sel];
    if (list && !list.some((a) => a.name === act)) {
      setSelectedAction(null);
      replaceRoute({ project: sel, service: null, action: null });
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
  setActionStates((m) => {
    let changed = false;
    const next: Record<string, ActionState[]> = {};
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
    sendWS({ type: "list_action_states", project: p });
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

  // Initialize selection from initial URL path
  const initRoute = parseRoute();
  if (initRoute.project) {
    setSelectedProject(initRoute.project);
    setSelectedService(initRoute.service);
    setSelectedAction(initRoute.action);
    expandProject(initRoute.project);
    if (initRoute.view === "git") {
      setActiveView("git");
      loadGitLog(initRoute.project);
      if (initRoute.commit) {
        selectCommit(initRoute.project, initRoute.commit, { skipPush: true });
      }
    } else {
      setActiveView("logs");
    }
    if (initRoute.service) {
      setKeyboardCursor({ kind: "service", project: initRoute.project, service: initRoute.service });
    } else if (initRoute.action) {
      setKeyboardCursor({ kind: "action", project: initRoute.project, action: initRoute.action });
    } else {
      setKeyboardCursor({ kind: "project", project: initRoute.project });
    }
  }

  const stopPopState = listenPopState((route) => {
    if (route.project) {
      if (route.view === "git") {
        setSelectedProject(route.project);
        setSelectedService(null);
        setSelectedAction(null);
        setActiveView("git");
        loadGitLog(route.project);
        if (route.commit) {
          selectCommit(route.project, route.commit, { skipPush: true });
        } else {
          setSelectedCommitHash((m) => ({ ...m, [route.project!]: null }));
        }
      } else if (route.service) {
        setActiveView("logs");
        selectService(route.project, route.service, { skipPush: true });
      } else if (route.action) {
        setActiveView("logs");
        selectAction(route.project, route.action, { skipPush: true });
      } else {
        setActiveView("logs");
        selectProject(route.project, { skipPush: true });
      }
    } else {
      setSelectedProject(null);
      setSelectedService(null);
      setSelectedAction(null);
      setActiveView("logs");
      setKeyboardCursor(null);
    }
  });

  onWS("projects", (resp) => {
    const next = resp.data as ProjectInfo[];
    const prevNames = new Set(untrack(projects).map((p) => p.name));
    setProjects((prev) => (sameProjectList(prev, next) ? prev : next));
    prune();
    // First paint (and newly appeared projects) need a services fetch —
    // refreshAll() often runs before projects arrive, when the list is empty.
    const collapsedSet = untrack(collapsed);
    for (const p of next) {
      if (!collapsedSet.has(p.name)) {
        if (!prevNames.has(p.name) || !untrack(services)[p.name]) {
          sendWS({ type: "list_services", project: p.name });
          sendWS({ type: "list_action_states", project: p.name });
        }
        if (!untrack(actions)[p.name]) {
          sendWS({ type: "list_actions", project: p.name });
        }
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
  onWS("actions", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = (resp.data ?? []) as ActionInfo[];
    setActions((m) => ({ ...m, [project]: next }));
  });
  onWS("git_commits", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) {
      setGitError((m) => ({ ...m, [project]: resp.error ?? "Failed to load git log" }));
      return;
    }
    if (resp.data) {
      if (Array.isArray(resp.data)) {
        setGitCommits((m) => ({ ...m, [project]: resp.data as GitCommit[] }));
      } else {
        const payload = resp.data as GitLogPayload;
        setGitCommits((m) => ({ ...m, [project]: payload.commits ?? [] }));
        setGitBranches((m) => ({ ...m, [project]: payload.branches ?? [] }));
        setGitTags((m) => ({ ...m, [project]: payload.tags ?? [] }));
        setGitStashes((m) => ({ ...m, [project]: payload.stashes ?? [] }));
      }
    }
  });
  onWS("git_diff", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitDiffLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) {
      setGitDiffError((m) => ({ ...m, [project]: resp.error ?? "Failed to load git diff" }));
      return;
    }
    const diffResult = resp.data as GitDiffResult;
    if (diffResult && diffResult.commit) {
      const hash = diffResult.commit.hash;
      setGitDiffs((m) => ({
        ...m,
        [project]: { ...(m[project] ?? {}), [hash]: diffResult },
      }));
    }
  });
  onWS("git_commit_result", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitCommitLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) {
      setGitCommitError((m) => ({ ...m, [project]: resp.error ?? "Failed to commit changes" }));
      return;
    }
    setGitCommitError((m) => ({ ...m, [project]: "" }));
    // Clear diff cache for WORKDIR and reload git log
    setGitDiffs((m) => {
      const copy = { ...(m[project] ?? {}) };
      delete copy["WORKDIR"];
      return { ...m, [project]: copy };
    });
    setSelectedCommitHash((m) => ({ ...m, [project]: null }));
    loadGitLog(project);
  });
  onWS("git_stage_result", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    // Clear diff cache for WORKDIR and reload WORKDIR diff
    setGitDiffs((m) => {
      const copy = { ...(m[project] ?? {}) };
      delete copy["WORKDIR"];
      return { ...m, [project]: copy };
    });
    loadGitDiff(project, "WORKDIR", undefined, true);
  });
  onWS("action_states", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = (resp.data ?? []) as ActionState[];
    setActionStates((m) => {
      const prev = m[project];
      if (prev && prev.length === next.length && JSON.stringify(prev) === JSON.stringify(next)) return m;
      return { ...m, [project]: next };
    });
  });
  onWS("action_state_changed", (resp) => {
    const next = resp.data as ActionState;
    const project = resp.project;
    if (!project) return;
    setActionStates((m) => {
      const list = m[project];
      if (!list) return { ...m, [project]: [next] };
      const updated = list.map((a) => (a.name === next.name ? next : a));
      return { ...m, [project]: updated };
    });
  });
  onWS("state_changed", (resp) => {
    const next = resp.data as ServiceState;
    const project = resp.project;
    if (!project) return;
    const updatedServices: Record<string, ServiceState[]> = {};
    setServices((m) => {
      const list = m[project];
      if (!list) return m;
      const updated = list.map((s) => (s.name === next.name ? next : s));
      if (sameServiceList(list, updated)) return m;
      updatedServices[project] = updated;
      return { ...m, [project]: updated };
    });
    if (updatedServices[project]) {
      const running = updatedServices[project].filter(
        (s) => s.status === "running" || s.status === "starting" || s.status === "backoff"
      ).length;
      setProjects((prev) =>
        prev.map((p) =>
          p.name === project
            ? { ...p, running_services: running, total_services: updatedServices[project].length }
            : p
        )
      );
    }
    pruneSelection();
  });
  onWS("action_done", (resp) => {
    if (resp.ok === false || resp.error) {
      pushToast(resp.error || `Action '${resp.action}' failed`, "error");
    } else {
      pushToast(`Action '${resp.action}' finished (exit code ${resp.exit_code ?? 0})`, "info");
    }
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

  return () => {
    stopOpen();
    stopPopState();
  };
}

export function refreshServices(project: string) {
  sendWS({ type: "list_services", project });
}

export function refreshActions(project: string) {
  sendWS({ type: "list_actions", project });
}

export function refreshActionStates(project: string) {
  sendWS({ type: "list_action_states", project });
}

export function runAction(project: string, actionName: string, args?: string[]) {
  selectAction(project, actionName, { skipPush: true });
  sendWS({ type: "run_action", project, action: actionName, args });
  pushToast(`Started action '${actionName}'`, "info");
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
  if (open) {
    refreshServices(name);
    refreshActions(name);
    refreshActionStates(name);
  }
}

export function expandProject(name: string) {
  setProjectExpanded(name, true);
}

export function selectProject(name: string, opts?: { skipPush?: boolean }) {
  setSelectedProject(name);
  setSelectedService(null);
  setSelectedAction(null);
  expandProject(name);
  setKeyboardCursor({ kind: "project", project: name });
  if (!opts?.skipPush) {
    pushRoute({ project: name, service: null, action: null });
  }
}

export function selectService(project: string, service: string, opts?: { skipPush?: boolean }) {
  setSelectedProject(project);
  setSelectedService(service);
  setSelectedAction(null);
  setActiveView("logs");
  expandProject(project);
  setKeyboardCursor({ kind: "service", project, service });
  if (!opts?.skipPush) {
    pushRoute({ project, service, action: null });
  }
}

export function selectAction(project: string, actionName: string, opts?: { skipPush?: boolean }) {
  setSelectedProject(project);
  setSelectedService(null);
  setSelectedAction(actionName);
  expandProject(project);
  setKeyboardCursor({ kind: "action", project, action: actionName });
  if (!opts?.skipPush) {
    pushRoute({ project, service: null, action: actionName });
  }
}

/** Whether the given service/action's log pane should show the previous run. */
export function isPreviousLogs(item: NavItem): boolean {
  return previousLogs().has(navItemKey(item));
}

/** Toggle between the live and previous-run log for a service or action. */
export function togglePreviousLogs(item: NavItem) {
  setPreviousLogs((prev) => {
    const next = new Set(prev);
    const key = navItemKey(item);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  });
}

/** Target of the previous-logs toggle: cursor service/action, else selection. */
export function previousTarget(): NavItem | null {
  const cur = keyboardCursor();
  if (cur?.kind === "service") return cur;
  if (cur?.kind === "action") return cur;
  const proj = selectedProject();
  const svc = selectedService();
  const act = selectedAction();
  if (proj && svc) return { kind: "service", project: proj, service: svc };
  if (proj && act) return { kind: "action", project: proj, action: act };
  return null;
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
  if (cur.kind === "service" || cur.kind === "action") {
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

export function startService(project: string, service: string) {
  sendWS({ type: "start_service", project, service });
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

function collectPaneIds(node: PaneNode): string[] {
  if (node.type === "terminal") return [node.id];
  return node.children.flatMap(collectPaneIds);
}

export function closeShellTab(project: string, tabId: string) {
  const state = getProjectShellState(project);
  const closingTab = state.tabs.find((t) => t.id === tabId);
  if (closingTab) {
    for (const id of collectPaneIds(closingTab.rootPane)) {
      closeTerminal(id);
    }
  }

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
  closeTerminal(targetPaneId);

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

export function reorderShellTabs(project: string, fromIndex: number, toIndex: number) {
  const state = getProjectShellState(project);
  if (
    fromIndex < 0 ||
    fromIndex >= state.tabs.length ||
    toIndex < 0 ||
    toIndex >= state.tabs.length ||
    fromIndex === toIndex
  ) {
    return;
  }
  const nextTabs = [...state.tabs];
  const [moved] = nextTabs.splice(fromIndex, 1);
  nextTabs.splice(toIndex, 0, moved);
  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: { ...state, tabs: nextTabs },
  }));
}

function updateSizesInTree(node: PaneNode, splitId: string, sizes: number[]): PaneNode {
  if (node.type === "terminal") return node;
  if (node.id === splitId) {
    return { ...node, sizes };
  }
  let changed = false;
  const newChildren = node.children.map((c) => {
    const updated = updateSizesInTree(c, splitId, sizes);
    if (updated !== c) changed = true;
    return updated;
  });
  return changed ? { ...node, children: newChildren } : node;
}

export function updateSplitSizes(project: string, splitId: string, sizes: number[]) {
  const state = getProjectShellState(project);
  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      ...state,
      tabs: state.tabs.map((t) => ({
        ...t,
        rootPane: updateSizesInTree(t.rootPane, splitId, sizes),
      })),
    },
  }));
}

function swapNodesInTree(node: PaneNode, idA: string, idB: string): PaneNode {
  if (node.type === "terminal") {
    if (node.id === idA) return { type: "terminal", id: idB };
    if (node.id === idB) return { type: "terminal", id: idA };
    return node;
  }
  return {
    ...node,
    children: node.children.map((c) => swapNodesInTree(c, idA, idB)),
  };
}

function insertNodeAtTarget(
  node: PaneNode,
  targetId: string,
  sourceNode: PaneNode,
  position: "left" | "right" | "top" | "bottom"
): PaneNode {
  if (node.type === "terminal") {
    if (node.id === targetId) {
      const direction = position === "left" || position === "right" ? "vertical" : "horizontal";
      const children =
        position === "left" || position === "top"
          ? [sourceNode, node]
          : [node, sourceNode];
      return {
        type: "split",
        id: `split-${Date.now()}-${Math.random()}`,
        direction,
        children,
      };
    }
    return node;
  }
  return {
    ...node,
    children: node.children.map((c) => insertNodeAtTarget(c, targetId, sourceNode, position)),
  };
}

export function moveShellPane(
  project: string,
  sourcePaneId: string,
  targetPaneId: string,
  position: "left" | "right" | "top" | "bottom" | "swap"
) {
  if (sourcePaneId === targetPaneId) return;
  const state = getProjectShellState(project);
  const activeTab = state.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;

  if (position === "swap") {
    const updatedRoot = swapNodesInTree(activeTab.rootPane, sourcePaneId, targetPaneId);
    setShellWorkspaces((prev) => ({
      ...prev,
      [project]: {
        ...state,
        tabs: state.tabs.map((t) => (t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t)),
      },
    }));
    return;
  }

  const sourceNode: PaneNode = { type: "terminal", id: sourcePaneId };
  const rootWithoutSource = removeNode(activeTab.rootPane, sourcePaneId);
  if (!rootWithoutSource) return;

  const updatedRoot = insertNodeAtTarget(rootWithoutSource, targetPaneId, sourceNode, position);
  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      ...state,
      tabs: state.tabs.map((t) => (t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t)),
    },
  }));
}

export function movePaneToNewTab(project: string, sourcePaneId: string) {
  const state = getProjectShellState(project);
  const tabWithPane = state.tabs.find((t) => collectPaneIds(t.rootPane).includes(sourcePaneId));
  if (!tabWithPane) return;

  const rootWithoutSource = removeNode(tabWithPane.rootPane, sourcePaneId);

  const newTab: ShellTab = {
    id: `tab-${Date.now()}`,
    title: `Shell ${state.tabs.length + 1}`,
    rootPane: { type: "terminal", id: sourcePaneId },
  };

  const updatedTabs = state.tabs
    .map((t) => {
      if (t.id === tabWithPane.id) {
        return rootWithoutSource ? { ...t, rootPane: rootWithoutSource } : null;
      }
      return t;
    })
    .filter((t): t is ShellTab => t !== null);

  updatedTabs.push(newTab);

  setShellWorkspaces((prev) => ({
    ...prev,
    [project]: {
      tabs: updatedTabs,
      activeTabId: newTab.id,
    },
  }));
}

export type PanelTab = "shell";

const [panelOpen, setPanelOpen] = createSignal(false);
const [panelTab, setPanelTab] = createSignal<PanelTab>("shell");
const [panelHeight, setPanelHeight] = createSignal(320);
const [panelMaximized, setPanelMaximized] = createSignal(false);

export function togglePanel() {
  setPanelOpen((open) => !open);
}

export function openPanelTab(tab: PanelTab) {
  setPanelTab(tab);
  setPanelOpen(true);
}

export function togglePanelMaximized() {
  setPanelMaximized((m) => !m);
}

/** Fetch the git commit log for a project, clearing any prior result/error. */
export function loadGitLog(name: string) {
  setGitLoading((m) => ({ ...m, [name]: true }));
  setGitError((m) => ({ ...m, [name]: "" }));
  sendWS({ type: "git_log", project: name });
}

/** Fetch diff for a commit in a project. */
export function loadGitDiff(project: string, hash: string, contextLines?: number, forceRefresh?: boolean) {
  const existing = gitDiffs()[project]?.[hash];
  if (existing && !forceRefresh && !contextLines) return;
  setGitDiffLoading((m) => ({ ...m, [project]: true }));
  setGitDiffError((m) => ({ ...m, [project]: "" }));
  sendWS({ type: "git_diff", project, hash, context_lines: contextLines });
}

/** Stage or unstage a file or all files in the working directory. */
export function stageGitFile(project: string, path: string, unstage?: boolean, stageAll?: boolean) {
  sendWS({ type: "git_stage", project, path, unstage, stage_all: stageAll });
}

/** Select a commit hash in the git view and fetch its diff. */
export function selectCommit(project: string, hash: string | null, opts?: { skipPush?: boolean }) {
  setSelectedCommitHash((m) => ({ ...m, [project]: hash }));
  setSelectedFilePath((m) => ({ ...m, [project]: null }));
  if (hash) {
    loadGitDiff(project, hash);
  }
  if (!opts?.skipPush && activeView() === "git") {
    pushRoute({ project, service: null, action: null, view: "git", commit: hash });
  }
}

/** Select a file path within the selected commit's diff. */
export function selectDiffFile(project: string, path: string | null) {
  setSelectedFilePath((m) => ({ ...m, [project]: path }));
}

/** Stage uncommitted changes and create a new commit. */
export function commitGitChanges(project: string, message: string) {
  if (!message.trim()) return;
  setGitCommitLoading((m) => ({ ...m, [project]: true }));
  setGitCommitError((m) => ({ ...m, [project]: "" }));
  sendWS({ type: "git_commit", project, message: message.trim() });
}

/** Open the full-page git view for a project and load its commit log. */
export function openGitView(name: string, commitHash?: string) {
  setSelectedProject(name);
  setSelectedService(null);
  setSelectedAction(null);
  setActiveView("git");
  loadGitLog(name);
  if (commitHash) {
    selectCommit(name, commitHash, { skipPush: true });
  }
  const commit = commitHash ?? selectedCommitHash()[name] ?? null;
  pushRoute({ project: name, service: null, action: null, view: "git", commit });
}

/** Close the git view and return to the project's default (logs) view. */
export function closeGitView() {
  setActiveView("logs");
  const p = selectedProject();
  if (p) {
    pushRoute({ project: p, service: selectedService(), action: selectedAction(), view: "logs" });
  } else {
    pushRoute({ project: null, service: null, action: null });
  }
}

export {
  theme,
  setTheme,
  projects,
  services,
  selectedProject,
  selectedService,
  selectedAction,
  activeView,
  setActiveView,
  panelOpen,
  setPanelOpen,
  panelTab,
  setPanelTab,
  panelHeight,
  setPanelHeight,
  panelMaximized,
  keyboardCursor,
  showHelp,
  wsStatus,
  start,
  toasts,
};

