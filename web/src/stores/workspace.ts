import { batch, createEffect, createMemo, createRoot, createSignal, untrack } from "solid-js";
import type { PaneNode, SplitNode, WorkspaceNode, WorkspaceTab } from "~/lib/types";
import { closeTerminal } from "~/lib/ws";

// --- identity ----------------------------------------------------------------

let idCounter = 0;

function newId(prefix: string): string {
  return `${prefix}-${++idCounter}`;
}

/** Scan persisted ids so new ids never collide with restored ones. */
function bumpIdCounterPast(id: string) {
  const match = /-(\d+)$/.exec(id);
  if (match) {
    const n = Number(match[1]);
    if (Number.isFinite(n) && n > idCounter) idCounter = n;
  }
}

export function serviceLogTabId(project: string, service: string): string {
  return `s:${project}/${service}`;
}

export function taskLogTabId(project: string, task: string): string {
  return `t:${project}/${task}`;
}

export function gitTabId(project: string): string {
  return `git:${project}`;
}

export function overviewTabId(project: string): string {
  return `ov:${project}`;
}

export function newTerminalTab(project: string): WorkspaceTab {
  const id = newId("tab");
  return { kind: "terminal", id, project, termId: id.replace("tab-", "term-") };
}

// --- state -------------------------------------------------------------------

function defaultPane(): PaneNode {
  const pane = newId("pane");
  return { type: "pane", id: pane, tabs: [], activeTabId: null };
}

const [root, setRoot] = createSignal<WorkspaceNode>(defaultPane());
/** The pane receiving new tabs and keyboard focus. Local to this browser. */
const [focusedPaneId, setFocusedPaneId] = createSignal<string>(untrack(root).type === "pane" ? (untrack(root) as PaneNode).id : "");

export { root, focusedPaneId };

// --- persistence (localStorage; the daemon sync in a later phase replaces this) ---

const STORAGE_KEY = "lc-workspace";
const SCHEMA_VERSION = 1;

interface PersistedWorkspace {
  v: number;
  root: WorkspaceNode;
  focusedPaneId: string;
}

function serialize(): string {
  return JSON.stringify({
    v: SCHEMA_VERSION,
    root: untrack(root),
    focusedPaneId: untrack(focusedPaneId),
  } satisfies PersistedWorkspace);
}

function loadPersisted() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return;
    const data = JSON.parse(raw) as PersistedWorkspace;
    if (data?.v !== SCHEMA_VERSION || !data.root || (data.root.type !== "split" && data.root.type !== "pane")) return;
    walkIds(data.root, bumpIdCounterPast);
    setRoot(data.root);
    setFocusedPaneId(
      panesIn(data.root).some((p) => p.id === data.focusedPaneId)
        ? data.focusedPaneId
        : (panesIn(data.root)[0]?.id ?? "")
    );
  } catch {
    // Corrupt or incompatible state: fall back to a fresh workspace.
  }
}

function walkIds(node: WorkspaceNode, fn: (id: string) => void) {
  fn(node.id);
  if (node.type === "split") node.children.forEach((c) => walkIds(c, fn));
}

let saveTimer: ReturnType<typeof setTimeout> | null = null;

createRoot(() => {
  createEffect(() => {
    root();
    focusedPaneId();
    if (saveTimer !== null) clearTimeout(saveTimer);
    saveTimer = setTimeout(() => {
      saveTimer = null;
      try {
        localStorage.setItem(STORAGE_KEY, serialize());
      } catch {
        // Storage full or unavailable: persistence is best-effort.
      }
    }, 250);
  });
});

loadPersisted();

// --- tree helpers ------------------------------------------------------------

export function panesIn(node: WorkspaceNode): PaneNode[] {
  if (node.type === "pane") return [node];
  return node.children.flatMap(panesIn);
}

function updateTree(fn: (node: WorkspaceNode) => WorkspaceNode | null) {
  setRoot((prev) => {
    const next = fn(prev);
    // The tree must always contain at least one pane.
    if (!next || countPanes(next) === 0) return defaultPane();
    return next;
  });
}

function countPanes(node: WorkspaceNode): number {
  return node.type === "pane" ? 1 : node.children.reduce((n, c) => n + countPanes(c), 0);
}

function mapPanes(node: WorkspaceNode, fn: (pane: PaneNode) => PaneNode | null): WorkspaceNode | null {
  if (node.type === "pane") return fn(node);
  const children = node.children.map((c) => mapPanes(c, fn)).filter((c): c is WorkspaceNode => c !== null);
  if (children.length === 0) return null;
  if (children.length === 1) return children[0]!;
  return { ...node, children };
}

function updatePane(paneId: string, fn: (pane: PaneNode) => PaneNode | null) {
  updateTree((node) => mapPanes(node, (p) => (p.id === paneId ? fn(p) : p)));
}

function findPane(paneId: string): PaneNode | null {
  return panesIn(untrack(root)).find((p) => p.id === paneId) ?? null;
}

/** The focused pane, falling back to the first pane if the id is stale. */
export const focusedPane = createMemo<PaneNode | null>(() => {
  const panes = panesIn(root());
  return panes.find((p) => p.id === focusedPaneId()) ?? panes[0] ?? null;
});

export const focusedPaneActiveTab = createMemo<WorkspaceTab | null>(() => {
  const pane = panesIn(root()).find((p) => p.id === focusedPaneId()) ?? panesIn(root())[0];
  if (!pane) return null;
  return pane.tabs.find((t) => t.id === pane.activeTabId) ?? pane.tabs[0] ?? null;
});

/** True when the focused pane's active tab is the git view of `project`. */
export function isGitTabFocused(project: string): boolean {
  const tab = untrack(focusedPaneActiveTab);
  return tab?.kind === "git" && tab.project === project;
}

/** True when the focused pane's active tab is the git view of `project`. */

/** Focuses the pane containing `tabId` and activates the tab there. */
export function focusTabById(tabId: string) {
  const pane = panesIn(untrack(root)).find((p) => p.tabs.some((t) => t.id === tabId));
  if (!pane) return;
  batchActivate(pane.id, tabId);
}

function batchActivate(paneId: string, tabId: string | null) {
  batch(() => {
    setFocusedPaneId(paneId);
    if (tabId !== null) updatePane(paneId, (p) => ({ ...p, activeTabId: tabId }));
  });
}

/**
 * Opens a tab in the focused pane (or focuses the existing one anywhere in
 * the tree). Log/git/overview tabs dedupe by stable id; terminal tabs are
 * always unique.
 */
export function openTab(tab: WorkspaceTab) {
  const tree = untrack(root);
  const owner = panesIn(tree).find((p) => p.tabs.some((t) => t.id === tab.id));
  if (owner) {
    batchActivate(owner.id, tab.id);
    return;
  }
  const paneId = untrack(focusedPaneId);
  const target = findPane(paneId) ?? panesIn(untrack(root))[0];
  if (!target) return;
  updatePane(target.id, (p) => ({
    ...p,
    tabs: [...p.tabs, tab],
    activeTabId: tab.id,
  }));
  setFocusedPaneId(target.id);
}

/** Opens (or focuses) a terminal tab for `project` in the focused pane. */
export function openTerminalTab(project: string): WorkspaceTab | null {
  const tab = newTerminalTab(project);
  openTab(tab);
  return tab;
}

export function selectTab(paneId: string, tabId: string) {
  batchActivate(paneId, tabId);
}

export function focusPane(paneId: string) {
  if (findPane(paneId)) setFocusedPaneId(paneId);
}

/** Reorders tabs within one pane's strip. */
export function reorderTab(paneId: string, fromIndex: number, toIndex: number) {
  updatePane(paneId, (p) => {
    if (fromIndex < 0 || toIndex < 0 || fromIndex >= p.tabs.length || toIndex >= p.tabs.length || fromIndex === toIndex) {
      return p;
    }
    const tabs = [...p.tabs];
    const [moved] = tabs.splice(fromIndex, 1);
    tabs.splice(toIndex, 0, moved!);
    return { ...p, tabs };
  });
}

/** Moves a tab into another pane's strip at `index` (append when omitted). */
export function moveTabToPane(tabId: string, targetPaneId: string, index?: number) {
  const tree = untrack(root);
  const source = panesIn(tree).find((p) => p.tabs.some((t) => t.id === tabId));
  const target = findPane(targetPaneId);
  if (!source || !target || source.id === targetPaneId) return;
  const tab = source.tabs.find((t) => t.id === tabId)!;
  const srcIdx = source.tabs.indexOf(tab);
  const activeAfterRemoval =
    source.activeTabId === tabId
      ? (source.tabs[srcIdx + 1] ?? source.tabs[srcIdx - 1])?.id ?? null
      : source.activeTabId;

  updateTree((node) => {
    const stripped = mapPanes(node, (p) => {
      if (p.id !== source.id) return p;
      const remaining = p.tabs.filter((t) => t.id !== tabId);
      // Empty source panes collapse away.
      if (remaining.length === 0) return null;
      return { ...p, tabs: remaining, activeTabId: activeAfterRemoval };
    });
    if (!stripped) return null;
    return insertTabIntoPane(stripped, targetPaneId, tab, index);
  });
  batchActivate(targetPaneId, tab.id);
}

function insertTabIntoPane(node: WorkspaceNode, paneId: string, tab: WorkspaceTab, index?: number): WorkspaceNode {
  if (node.type === "pane") {
    if (node.id !== paneId) return node;
    const at = index === undefined || index < 0 || index > node.tabs.length ? node.tabs.length : index;
    return { ...node, tabs: [...node.tabs.slice(0, at), tab, ...node.tabs.slice(at)], activeTabId: tab.id };
  }
  return { ...node, children: node.children.map((c) => insertTabIntoPane(c, paneId, tab, index)) };
}

export function closeTab(tabId: string) {
  const tree = untrack(root);
  const owner = panesIn(tree).find((p) => p.tabs.some((t) => t.id === tabId));
  if (!owner) return;
  const tab = owner.tabs.find((t) => t.id === tabId)!;
  if (tab.kind === "terminal") closeTerminal(tab.termId);

  const idx = owner.tabs.findIndex((t) => t.id === tabId);
  const neighbor = owner.tabs[idx + 1] ?? owner.tabs[idx - 1];
  const nextActiveId = owner.activeTabId === tabId ? neighbor?.id ?? null : owner.activeTabId;

  updateTree((node) =>
    mapPanes(node, (p) => {
      if (p.id !== owner.id) return p;
      const tabs = p.tabs.filter((t) => t.id !== tabId);
      if (tabs.length === 0) return null; // pane collapses away
      return { ...p, tabs, activeTabId: nextActiveId };
    })
  );
}

// --- pane operations ---------------------------------------------------------

/** Splits `paneId`, inserting the new pane after it. `seed` tabs the new pane. */
export function splitPane(paneId: string, direction: "horizontal" | "vertical", seed?: WorkspaceTab) {
  updateTree((node) => splitNodeAt(node, paneId, direction, seed));
  // Focus lands on the new pane (the sibling right after the split target).
  const panes = panesIn(untrack(root));
  const targetIdx = panes.findIndex((p) => p.id === paneId);
  if (targetIdx >= 0 && panes[targetIdx + 1]) setFocusedPaneId(panes[targetIdx + 1]!.id);
}

function newPaneNode(): PaneNode {
  return { type: "pane", id: newId("pane"), tabs: [], activeTabId: null };
}

function splitNodeAt(node: WorkspaceNode, paneId: string, direction: "horizontal" | "vertical", seed?: WorkspaceTab): WorkspaceNode {
  if (node.type === "pane") {
    if (node.id !== paneId) return node;
    const fresh = newPaneNode();
    if (seed) {
      fresh.tabs = [seed];
      fresh.activeTabId = seed.id;
    }
    return {
      type: "split",
      id: newId("split"),
      direction,
      children: [node, fresh],
    };
  }
  return { ...node, children: node.children.map((c) => splitNodeAt(c, paneId, direction, seed)) };
}

/** Closes a pane and every tab in it; sibling panes absorb the space. */
export function closePane(paneId: string) {
  const pane = findPane(paneId);
  if (!pane) return;
  for (const t of pane.tabs) {
    if (t.kind === "terminal") closeTerminal(t.termId);
  }
  updateTree((node) => {
    const removed = removePaneFromTree(node, paneId);
    return removed;
  });
}

function removePaneFromTree(node: WorkspaceNode, paneId: string): WorkspaceNode | null {
  if (node.type === "pane") return node.id === paneId ? null : node;
  const children = node.children
    .map((c) => removePaneFromTree(c, paneId))
    .filter((c): c is WorkspaceNode => c !== null);
  if (children.length === 0) return null;
  if (children.length === 1) return children[0]!;
  return { ...node, children };
}

export function updateSizes(splitId: string, sizes: number[]) {
  updateTree((node) => updateSizesInTree(node, splitId, sizes));
}

function updateSizesInTree(node: WorkspaceNode, splitId: string, sizes: number[]): WorkspaceNode {
  if (node.type === "pane") return node;
  let changed = node.id === splitId;
  const self: SplitNode = changed ? { ...node, sizes } : node;
  const children = self.children.map((c) => {
    const updated = updateSizesInTree(c, splitId, sizes);
    if (updated !== c) changed = true;
    return updated;
  });
  return changed ? { ...self, children } : self;
}

// --- previous-run mode -------------------------------------------------------

export function isPreviousLogs(tabId: string): boolean {
  const tab = findTabById(tabId);
  return tab?.kind === "log-service" || tab?.kind === "log-task" ? !!tab.previous : false;
}

export function togglePreviousLogs(tabId: string) {
  updateTree((node) =>
    mapPanes(node, (p) => {
      if (!p.tabs.some((t) => t.id === tabId)) return p;
      return {
        ...p,
        tabs: p.tabs.map((t) =>
          t.id === tabId && (t.kind === "log-service" || t.kind === "log-task")
            ? ({ ...t, previous: !t.previous } as WorkspaceTab)
            : t
        ),
      };
    })
  );
}

// --- pruning -----------------------------------------------------------------

/** Drops tabs whose target no longer exists (project removed / service deleted). */
export function pruneWorkspace(keepTab: (tab: WorkspaceTab) => boolean) {
  updateTree((node) =>
    mapPanes(node, (p) => {
      const tabs = p.tabs.filter(keepTab);
      if (tabs.length === p.tabs.length) return p;
      for (const t of p.tabs) {
        if (t.kind === "terminal" && !keepTab(t)) closeTerminal(t.termId);
      }
      const activeKept = tabs.some((t) => t.id === p.activeTabId);
      return tabs.length === 0
        ? null
        : { ...p, tabs, activeTabId: activeKept ? p.activeTabId : tabs[0]!.id };
    })
  );
  // Focus fallback if the focused pane vanished.
  if (!findPane(untrack(focusedPaneId))) {
    const first = panesIn(untrack(root))[0];
    if (first) setFocusedPaneId(first.id);
  }
}

// --- convenience accessors ---------------------------------------------------

export function findTabById(tabId: string): WorkspaceTab | null {
  for (const pane of panesIn(untrack(root))) {
    const tab = pane.tabs.find((t) => t.id === tabId);
    if (tab) return tab;
  }
  return null;
}

/** All tabs in the tree, in pane order — used by the mobile flat strip. */
export function allTabs(): WorkspaceTab[] {
  return panesIn(untrack(root)).flatMap((p) => p.tabs);
}

/** The pane whose active tab is `tabId`, if any. */
export function paneOfTab(tabId: string): PaneNode | null {
  return panesIn(untrack(root)).find((p) => p.tabs.some((t) => t.id === tabId)) ?? null;
}
