import { untrack } from "solid-js";
import { createSignal } from "solid-js";
import type { PaneNode, ShellTab, SplitPaneNode } from "~/lib/types";
import { closeTerminal } from "~/lib/ws";
import { selectedProject } from "~/stores/nav";

export interface ProjectShellState {
  tabs: ShellTab[];
  activeTabId: string;
}

const [shellWorkspaces, setShellWorkspaces] = createSignal<Record<string, ProjectShellState>>({});
export { shellWorkspaces };

let idCounter = 0;

function newTab(index: number): ShellTab {
  ++idCounter;
  return {
    id: `tab-${idCounter}`,
    title: `Shell ${index}`,
    rootPane: { type: "terminal", id: `term-${idCounter}` },
  };
}

/**
 * Creates a workspace for the project if none exists yet. Called eagerly when
 * a project becomes selected so component memos never mutate state.
 */
export function ensureShellWorkspace(project: string): ProjectShellState {
  const current = shellWorkspaces()[project];
  if (current && current.tabs.length > 0) return current;
  const tab = newTab(1);
  const state: ProjectShellState = { tabs: [tab], activeTabId: tab.id };
  setShellWorkspaces((prev) => ({ ...prev, [project]: state }));
  return state;
}

/** Read-only accessor for components; returns null before ensure ran. */
export function shellStateOf(project: string | null): ProjectShellState | null {
  if (!project) return null;
  return shellWorkspaces()[project] ?? null;
}

function updateProject(project: string, fn: (state: ProjectShellState) => ProjectShellState) {
  setShellWorkspaces((prev) => {
    const current = prev[project];
    if (!current) return prev;
    return { ...prev, [project]: fn(current) };
  });
}

export function addShellTab(project: string) {
  const state = untrack(() => shellStateOf(project));
  if (!state) return;
  const tab = newTab(state.tabs.length + 1);
  updateProject(project, (s) => ({
    tabs: [...s.tabs, tab],
    activeTabId: tab.id,
  }));
}

export function selectShellTab(project: string, tabId: string) {
  updateProject(project, (s) => ({ ...s, activeTabId: tabId }));
}

function collectPaneIds(node: PaneNode): string[] {
  if (node.type === "terminal") return [node.id];
  return node.children.flatMap(collectPaneIds);
}

export function closeShellTab(project: string, tabId: string) {
  const state = untrack(() => shellStateOf(project));
  if (!state) return;
  const closingTab = state.tabs.find((t) => t.id === tabId);
  if (closingTab) {
    for (const id of collectPaneIds(closingTab.rootPane)) {
      closeTerminal(id);
    }
  }

  const remaining = state.tabs.filter((t) => t.id !== tabId);
  if (remaining.length === 0) {
    const tab = newTab(1);
    updateProject(project, () => ({ tabs: [tab], activeTabId: tab.id }));
    return;
  }
  const nextActive =
    state.activeTabId === tabId ? remaining[remaining.length - 1]!.id : state.activeTabId;
  updateProject(project, () => ({ tabs: remaining, activeTabId: nextActive }));
}

function splitNode(node: PaneNode, targetId: string, direction: "horizontal" | "vertical", newPaneId: string): PaneNode {
  ++idCounter;
  const split: SplitPaneNode = {
    type: "split",
    id: `split-${idCounter}`,
    direction,
    children: [],
  };
  if (node.type === "terminal") {
    if (node.id === targetId) {
      split.children = [node, { type: "terminal", id: newPaneId }];
      return split;
    }
    return node;
  }
  return {
    ...node,
    children: node.children.map((child) => splitNode(child, targetId, direction, newPaneId)),
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
  if (nextChildren.length === 1) return nextChildren[0]!;
  return { ...node, children: nextChildren };
}

export function splitShellPane(project: string, targetPaneId: string, direction: "horizontal" | "vertical") {
  const state = untrack(() => shellStateOf(project));
  const activeTab = state?.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;
  ++idCounter;
  const updatedRoot = splitNode(activeTab.rootPane, targetPaneId, direction, `term-${idCounter}`);
  updateProject(project, (s) => ({
    ...s,
    tabs: s.tabs.map((t) => (t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t)),
  }));
}

export function closeShellPane(project: string, targetPaneId: string) {
  closeTerminal(targetPaneId);
  const state = untrack(() => shellStateOf(project));
  const activeTab = state?.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;
  const updatedRoot = removeNode(activeTab.rootPane, targetPaneId);
  if (!updatedRoot) {
    closeShellTab(project, activeTab.id);
    return;
  }
  updateProject(project, (s) => ({
    ...s,
    tabs: s.tabs.map((t) => (t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t)),
  }));
}

export function reorderShellTabs(project: string, fromIndex: number, toIndex: number) {
  const state = untrack(() => shellStateOf(project));
  if (!state || fromIndex < 0 || fromIndex >= state.tabs.length || toIndex < 0 || toIndex >= state.tabs.length || fromIndex === toIndex) {
    return;
  }
  const nextTabs = [...state.tabs];
  const [moved] = nextTabs.splice(fromIndex, 1);
  nextTabs.splice(toIndex, 0, moved!);
  updateProject(project, (s) => ({ ...s, tabs: nextTabs }));
}

function updateSizesInTree(node: PaneNode, splitId: string, sizes: number[]): PaneNode {
  if (node.type === "terminal") return node;
  let changed = false;
  const matched = node.id === splitId;
  if (matched) changed = true;
  const self: SplitPaneNode = matched ? { ...node, sizes } : node;
  const newChildren = self.children.map((c) => {
    const updated = updateSizesInTree(c, splitId, sizes);
    if (updated !== c) changed = true;
    return updated;
  });
  return changed ? { ...self, children: newChildren } : self;
}

export function updateSplitSizes(project: string, splitId: string, sizes: number[]) {
  updateProject(project, (s) => ({
    ...s,
    tabs: s.tabs.map((t) => ({ ...t, rootPane: updateSizesInTree(t.rootPane, splitId, sizes) })),
  }));
}

function swapNodesInTree(node: PaneNode, idA: string, idB: string): PaneNode {
  if (node.type === "terminal") {
    if (node.id === idA) return { type: "terminal", id: idB };
    if (node.id === idB) return { type: "terminal", id: idA };
    return node;
  }
  return { ...node, children: node.children.map((c) => swapNodesInTree(c, idA, idB)) };
}

function insertNodeAtTarget(
  node: PaneNode,
  targetId: string,
  sourceNode: PaneNode,
  position: "left" | "right" | "top" | "bottom"
): PaneNode {
  ++idCounter;
  if (node.type === "terminal") {
    if (node.id !== targetId) return node;
    const direction = position === "left" || position === "right" ? "vertical" : "horizontal";
    const children =
      position === "left" || position === "top"
        ? [sourceNode, node]
        : [node, sourceNode];
    return { type: "split", id: `split-${idCounter}`, direction, children };
  }
  return { ...node, children: node.children.map((c) => insertNodeAtTarget(c, targetId, sourceNode, position)) };
}

export function moveShellPane(
  project: string,
  sourcePaneId: string,
  targetPaneId: string,
  position: "left" | "right" | "top" | "bottom" | "swap"
) {
  if (sourcePaneId === targetPaneId) return;
  const state = untrack(() => shellStateOf(project));
  const activeTab = state?.tabs.find((t) => t.id === state.activeTabId);
  if (!activeTab) return;

  let updatedRoot: PaneNode;
  if (position === "swap") {
    updatedRoot = swapNodesInTree(activeTab.rootPane, sourcePaneId, targetPaneId);
  } else {
    const rootWithoutSource = removeNode(activeTab.rootPane, sourcePaneId);
    if (!rootWithoutSource) return;
    updatedRoot = insertNodeAtTarget(rootWithoutSource, targetPaneId, { type: "terminal", id: sourcePaneId }, position);
  }
  updateProject(project, (s) => ({
    ...s,
    tabs: s.tabs.map((t) => (t.id === activeTab.id ? { ...t, rootPane: updatedRoot } : t)),
  }));
}

export function movePaneToNewTab(project: string, sourcePaneId: string) {
  const state = untrack(() => shellStateOf(project));
  const tabWithPane = state?.tabs.find((t) => collectPaneIds(t.rootPane).includes(sourcePaneId));
  if (!state || !tabWithPane) return;

  const rootWithoutSource = removeNode(tabWithPane.rootPane, sourcePaneId);
  const tab = newTab(state.tabs.length + 1);
  tab.rootPane = { type: "terminal", id: sourcePaneId };

  const updatedTabs = state.tabs
    .map((t) =>
      t.id === tabWithPane.id ? (rootWithoutSource ? { ...t, rootPane: rootWithoutSource } : null) : t
    )
    .filter((t): t is ShellTab => t !== null);
  updatedTabs.push(tab);

  updateProject(project, () => ({ tabs: updatedTabs, activeTabId: tab.id }));
}

/** Convenience for components: workspace of the selected project. */
export function selectedShellState(): ProjectShellState | null {
  return shellStateOf(selectedProject());
}
