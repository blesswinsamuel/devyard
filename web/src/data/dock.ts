import { createRoot, createEffect } from "solid-js";
import { createStore, produce } from "solid-js/store";
import { readPersisted, writePersisted } from "~/lib/persistence";
import type { LogSourceRef } from "./logs";

/** Bottom dock: terminals, attached sessions and pinned log streams. It lives
 * in the app shell, so tabs (and their sessions) survive navigation. */
export type DockTab =
  | { id: string; kind: "terminal"; project: string; title: string; sessionId?: string }
  | { id: string; kind: "attach"; target: "task" | "service"; project: string; name: string; title: string }
  | { id: string; kind: "logs"; project: string; sources: LogSourceRef[]; title: string };

export interface DockState {
  open: boolean;
  /** Height in px of the (desktop) dock panel. */
  height: number;
  tabs: DockTab[];
  /** Tab shown in each pane; pane 1 is non-null when split. */
  panes: [string | null, string | null];
  focused: 0 | 1;
}

export const DOCK_MIN_HEIGHT = 160;
const DEFAULT_HEIGHT = 320;

const newId = () => Math.random().toString(36).slice(2, 10);

function parseDock(raw: unknown): DockState | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const r = raw as Partial<DockState>;
  const tabs = Array.isArray(r.tabs)
    ? r.tabs.filter(
        (t): t is DockTab =>
          !!t && typeof t.id === "string" && typeof t.project === "string" && ["terminal", "attach", "logs"].includes(t.kind),
      )
    : [];
  const ids = new Set(tabs.map((t) => t.id));
  const pane = (v: unknown) => (typeof v === "string" && ids.has(v) ? v : null);
  const panes: [string | null, string | null] = [pane(r.panes?.[0]), pane(r.panes?.[1])];
  if (!panes[0]) panes[0] = tabs[0]?.id ?? null;
  if (panes[1] === panes[0]) panes[1] = null;
  return {
    open: r.open === true && tabs.length > 0,
    height: typeof r.height === "number" ? Math.max(DOCK_MIN_HEIGHT, r.height) : DEFAULT_HEIGHT,
    tabs,
    panes,
    focused: r.focused === 1 && panes[1] ? 1 : 0,
  };
}

const initial: DockState = { open: false, height: DEFAULT_HEIGHT, tabs: [], panes: [null, null], focused: 0 };

export const [dock, setDock] = createStore<DockState>(readPersisted("dock", initial, parseDock));

createRoot(() => {
  createEffect(() => writePersisted("dock", JSON.parse(JSON.stringify(dock))));
});

function show(id: string) {
  setDock(
    produce((s) => {
      s.open = true;
      if (s.panes[0] === id || s.panes[1] === id) {
        s.focused = s.panes[1] === id ? 1 : 0;
        return;
      }
      s.panes[s.focused] = id;
    }),
  );
}

function add(tab: DockTab) {
  setDock("tabs", (tabs) => [...tabs, tab]);
  show(tab.id);
}

export const dockActions = {
  openTerminal(project: string) {
    const n = dock.tabs.filter((t) => t.kind === "terminal" && t.project === project).length + 1;
    add({ id: newId(), kind: "terminal", project, title: n > 1 ? `${project} (${n})` : project });
  },
  attach(target: "task" | "service", project: string, name: string) {
    const existing = dock.tabs.find(
      (t) => t.kind === "attach" && t.target === target && t.project === project && t.name === name,
    );
    if (existing) return show(existing.id);
    add({ id: newId(), kind: "attach", target, project, name, title: name });
  },
  pinLogs(project: string, sources: LogSourceRef[], title: string) {
    add({ id: newId(), kind: "logs", project, sources, title });
  },
  activate: show,
  setSessionId(id: string, sessionId: string) {
    setDock("tabs", (t) => t.id === id, produce((t) => void (t.kind === "terminal" && (t.sessionId = sessionId))));
  },
  close(id: string) {
    setDock(
      produce((s) => {
        const idx = s.tabs.findIndex((t) => t.id === id);
        if (idx < 0) return;
        s.tabs.splice(idx, 1);
        const fallback = s.tabs.find((t) => t.id !== s.panes[0] && t.id !== s.panes[1])?.id ?? null;
        if (s.panes[1] === id) s.panes[1] = null;
        if (s.panes[0] === id) {
          s.panes[0] = s.panes[1] ?? fallback ?? s.tabs[Math.max(0, idx - 1)]?.id ?? null;
          if (s.panes[1] === s.panes[0]) s.panes[1] = null;
        }
        if (!s.panes[1]) s.focused = 0;
        if (!s.tabs.length) s.open = false;
      }),
    );
  },
  toggle() {
    setDock("open", (o) => !o && dock.tabs.length > 0);
  },
  setOpen(open: boolean) {
    setDock("open", open && dock.tabs.length > 0);
  },
  setHeight(height: number) {
    setDock("height", Math.max(DOCK_MIN_HEIGHT, Math.round(height)));
  },
  toggleSplit() {
    setDock(
      produce((s) => {
        if (s.panes[1]) {
          s.panes[1] = null;
          s.focused = 0;
          return;
        }
        const other = s.tabs.find((t) => t.id !== s.panes[0]);
        if (!other) return;
        s.panes[1] = other.id;
        s.focused = 1;
      }),
    );
  },
  focusPane(pane: 0 | 1) {
    if (pane === 1 && !dock.panes[1]) return;
    setDock("focused", pane);
  },
};
