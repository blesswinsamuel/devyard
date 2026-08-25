import { batch, createEffect, createRoot, createSignal } from "solid-js";

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

// --- toasts ---------------------------------------------------------------

export interface Toast {
  id: number;
  message: string;
  kind: "error" | "info" | "success";
}

const [toasts, setToasts] = createSignal<Toast[]>([]);
export { toasts };

let nextToastId = 0;
const toastTimers = new Map<number, ReturnType<typeof setTimeout>>();
const TOAST_MAX = 5;
const TOAST_TTL = 5000;

function disarm(id: number) {
  const t = toastTimers.get(id);
  if (t !== undefined) {
    clearTimeout(t);
    toastTimers.delete(id);
  }
}

function dismiss(id: number) {
  disarm(id);
  setToasts((list) => list.filter((x) => x.id !== id));
}

/**
 * Pushes a toast, collapsing repeats of the same message into the existing
 * entry (re-arming its timer) so a failing poll can't flood the screen.
 */
export function pushToast(message: string, kind: Toast["kind"] = "error") {
  const existing = toasts().find((t) => t.message === message);
  if (existing) {
    disarm(existing.id);
    toastTimers.set(
      existing.id,
      setTimeout(() => dismiss(existing.id), TOAST_TTL)
    );
    return;
  }
  const id = ++nextToastId;
  setToasts((list) => {
    const trimmed = list.length >= TOAST_MAX ? list.slice(list.length - TOAST_MAX + 1) : list;
    return [...trimmed, { id, message, kind }];
  });
  toastTimers.set(id, setTimeout(() => dismiss(id), TOAST_TTL));
}

export { dismiss as dismissToast };

// --- overlays -------------------------------------------------------------

/** Mobile-only: whether the project sidebar drawer is slid in. */
const [sidebarOpen, setSidebarOpen] = createSignal(false);

export { sidebarOpen, setSidebarOpen };

const [showPortsModal, setShowPortsModal] = createSignal(false);
/** Which scope the ports dialog was opened in: current project or all projects. */
const [portsScope, setPortsScope] = createSignal<"project" | "all">("project");
const [showAddProject, setShowAddProject] = createSignal(false);
const [showDaemonModal, setShowDaemonModal] = createSignal(false);
const [showHelp, setShowHelp] = createSignal(false);

export {
  showPortsModal,
  setShowPortsModal,
  portsScope,
  showAddProject,
  setShowAddProject,
  showDaemonModal,
  setShowDaemonModal,
  showHelp,
  setShowHelp,
};

/** Opens the ports dialog pre-scoped to one project or to every project. */
export function openPortsModal(scope: "project" | "all") {
  batch(() => {
    setPortsScope(scope);
    setShowPortsModal(true);
  });
}

export function toggleHelp() {
  setShowHelp((v) => !v);
}

export function closeHelp() {
  setShowHelp(false);
}

export function anyOverlayOpen(): boolean {
  return showPortsModal() || showAddProject() || showDaemonModal() || showHelp();
}

// --- bottom terminal panel --------------------------------------------------

const storedHeight = Number(localStorage.getItem("lc-panel-height")) || 320;

const [panelOpen, setPanelOpen] = createSignal(false);
const [panelHeight, setPanelHeightRaw] = createSignal(storedHeight);
const [panelMaximized, setPanelMaximized] = createSignal(false);

export { panelOpen, setPanelOpen, panelHeight, panelMaximized, theme, setTheme };

export function setPanelHeight(px: number) {
  setPanelHeightRaw(px);
  localStorage.setItem("lc-panel-height", String(Math.round(px)));
}

export function togglePanel() {
  setPanelOpen((open) => !open);
}

export function togglePanelMaximized() {
  setPanelMaximized((m) => !m);
}

/** Open the terminal panel for the currently selected project. */
export function openTerminalPanel() {
  batch(() => {
    setPanelOpen(true);
    setPanelMaximized(false);
  });
}
