import { createEffect, createRoot, createSignal, untrack } from "solid-js";
import type { ProjectInfo, ServiceState } from "./types";
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

const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
const [services, setServices] = createSignal<Record<string, ServiceState[]>>({});
const [expanded, setExpanded] = createSignal<Set<string>>(new Set());
const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
const [selectedService, setSelectedService] = createSignal<string | null>(null);
const [toasts, setToasts] = createSignal<{ id: number; message: string; kind: "error" | "info" }[]>([]);

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
  const sel = selectedProject();
  if (sel && !names.has(sel)) {
    setSelectedProject(null);
    setSelectedService(null);
  }
}

function refreshAll() {
  sendWS({ type: "list_projects" });
  const needed = new Set<string>();
  expanded().forEach((p) => {
    needed.add(p);
  });
  const sel = untrack(selectedProject);
  if (sel) {
    needed.add(sel);
  }
  needed.forEach((p) => {
    sendWS({ type: "list_services", project: p });
  });
}

function start() {
  connectWS();
  onWS("projects", (resp) => {
    const next = resp.data as ProjectInfo[];
    setProjects((prev) => (sameProjectList(prev, next) ? prev : next));
    prune();
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
  });
  onWS("error", (resp) => {
    if (resp.error) pushToast(resp.error);
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

export function toggleProject(name: string) {
  setExpanded((prev) => {
    const next = new Set(prev);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    return next;
  });
  refreshServices(name);
}

export function expandProject(name: string) {
  setExpanded((prev) => (prev.has(name) ? prev : new Set(prev).add(name)));
  refreshServices(name);
}

export function selectProject(name: string) {
  setSelectedProject(name);
  setSelectedService(null);
  expandProject(name);
}

export function selectService(project: string, service: string) {
  setSelectedProject(project);
  setSelectedService(service);
  expandProject(project);
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

export {
  theme,
  setTheme,
  projects,
  services,
  expanded,
  selectedProject,
  selectedService,
  wsStatus,
  start,
  toasts,
};
