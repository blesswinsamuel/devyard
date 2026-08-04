import { createEffect, createRoot, createSignal, untrack } from "solid-js";
import type { ProjectInfo, ServiceState } from "./types";
import { sendWS, onWS, wsStatus, connectWS } from "./ws";

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

function prune() {
  const names = new Set(projects().map((p) => p.name));
  setServices((m) => {
    const next: Record<string, ServiceState[]> = {};
    for (const [k, v] of Object.entries(m)) if (names.has(k)) next[k] = v;
    return next;
  });
  const sel = selectedProject();
  if (sel && !names.has(sel)) {
    setSelectedProject(null);
    setSelectedService(null);
  }
}

function start() {
  connectWS();
  onWS("projects", (resp) => {
    setProjects(resp.data as ProjectInfo[]);
    prune();
  });
  onWS("services", (resp) => {
    if (resp.project) {
      setServices((m) => ({ ...m, [resp.project!]: resp.data as ServiceState[] }));
    }
  });
  onWS("error", (resp) => {
    if (resp.error) pushToast(resp.error);
  });

  const poll = setInterval(() => {
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
  }, 2000);

  return () => clearInterval(poll);
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
}

export function stopService(project: string, service: string) {
  sendWS({ type: "stop_service", project, service });
}

export function killService(project: string, service: string, signal = "SIGKILL") {
  sendWS({ type: "kill_service", project, service, signal });
}

export function stopProject(project: string) {
  sendWS({ type: "stop_project", project });
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
