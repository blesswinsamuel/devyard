import type { GitEntity, ProjectEntity, ServiceEntity, TaskEntity } from "~/data/entities";

export interface TreeNode {
  id: string;
  kind: "project" | "service" | "task";
  level: 1 | 2;
  project: string;
  name?: string;
  parentId?: string;
  expanded?: boolean;
  posinset: number;
  setsize: number;
}

export interface TreeInput {
  projects: ProjectEntity[];
  servicesOf: (project: string) => ServiceEntity[];
  tasksOf: (project: string) => TaskEntity[];
  expanded: (project: string) => boolean;
  filter: string;
}

export const nodeId = (kind: TreeNode["kind"], project: string, name?: string) =>
  name ? `${kind}:${project}/${name}` : `${kind}:${project}`;

/**
 * Flattens projects → services/tasks into visible rows. A filter matches
 * names case-insensitively; a matching project shows all its children,
 * otherwise only matching children are shown (and their project is
 * force-expanded).
 */
export function buildTree(input: TreeInput): TreeNode[] {
  const q = input.filter.trim().toLowerCase();
  const matches = (s: string) => !q || s.toLowerCase().includes(q);
  const out: TreeNode[] = [];
  const visibleProjects: { p: ProjectEntity; children: { kind: "service" | "task"; name: string }[]; force: boolean }[] = [];
  for (const p of input.projects) {
    const all = [
      ...input.servicesOf(p.id).map((s) => ({ kind: "service" as const, name: s.name })),
      ...input.tasksOf(p.id).map((t) => ({ kind: "task" as const, name: t.name })),
    ];
    const projectMatch = matches(p.id);
    const children = projectMatch ? all : all.filter((c) => matches(c.name));
    if (!projectMatch && children.length === 0) continue;
    visibleProjects.push({ p, children, force: !!q && !projectMatch });
  }
  visibleProjects.forEach(({ p, children, force }, i) => {
    const id = nodeId("project", p.id);
    const expanded = force || (!!q && children.length > 0) || input.expanded(p.id);
    out.push({ id, kind: "project", level: 1, project: p.id, expanded, posinset: i + 1, setsize: visibleProjects.length });
    if (!expanded) return;
    children.forEach((c, j) =>
      out.push({
        id: nodeId(c.kind, p.id, c.name),
        kind: c.kind,
        level: 2,
        project: p.id,
        name: c.name,
        parentId: id,
        posinset: j + 1,
        setsize: children.length,
      }),
    );
  });
  return out;
}

/** Keyboard navigation per the WAI-ARIA tree pattern. Returns the new focus
 * id and an optional expand/collapse or activation request. */
export function treeKey(
  nodes: TreeNode[],
  focusedId: string | undefined,
  key: string,
): { focus?: string; toggle?: { project: string; expanded: boolean }; activate?: TreeNode } | null {
  if (!nodes.length) return null;
  const idx = Math.max(0, nodes.findIndex((n) => n.id === focusedId));
  const node = nodes[idx]!;
  switch (key) {
    case "ArrowDown":
      return { focus: nodes[Math.min(nodes.length - 1, idx + 1)]!.id };
    case "ArrowUp":
      return { focus: nodes[Math.max(0, idx - 1)]!.id };
    case "Home":
      return { focus: nodes[0]!.id };
    case "End":
      return { focus: nodes[nodes.length - 1]!.id };
    case "ArrowRight":
      if (node.kind === "project") {
        if (!node.expanded) return { focus: node.id, toggle: { project: node.project, expanded: true } };
        const child = nodes[idx + 1];
        return child?.parentId === node.id ? { focus: child.id } : { focus: node.id };
      }
      return { focus: node.id };
    case "ArrowLeft":
      if (node.kind === "project")
        return node.expanded ? { focus: node.id, toggle: { project: node.project, expanded: false } } : { focus: node.id };
      return { focus: node.parentId };
    case "Enter":
    case " ":
      return { focus: node.id, activate: node };
    default:
      return null;
  }
}

export function gitSummary(g: GitEntity | undefined): string {
  if (!g?.isRepo) return "";
  const parts = [g.branch || g.headHash.slice(0, 7)];
  if (g.ahead) parts.push(`↑${g.ahead}`);
  if (g.behind) parts.push(`↓${g.behind}`);
  return parts.join(" ");
}

/**
 * The index to move `dragged` to when it is dropped before or after `target`
 * in a list ordered as `order`: the position in the list without the dragged
 * project, which is what the daemon expects. Null when the drop would not
 * change anything.
 */
export function dropIndex(order: string[], dragged: string, target: string, after: boolean): number | null {
  const from = order.indexOf(dragged);
  if (from < 0 || dragged === target) return null;
  const rest = order.filter((id) => id !== dragged);
  const at = rest.indexOf(target);
  if (at < 0) return null;
  const index = at + (after ? 1 : 0);
  return index === from ? null : index;
}
