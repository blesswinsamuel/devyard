export interface GraphInput {
  name: string;
  deps: { name: string; condition: string }[];
}

export interface GraphNode {
  name: string;
  layer: number;
  row: number;
}

export interface GraphEdge {
  from: string;
  to: string;
  condition: string;
}

/**
 * Layered layout: a node's layer is 1 + the deepest dependency's layer, so
 * dependencies are always to the left of their dependents. Cycles (rejected
 * by config validation, but be safe) and unknown deps are ignored.
 */
export function layoutGraph(items: GraphInput[]): { nodes: GraphNode[]; edges: GraphEdge[]; layers: number; rows: number } {
  const byName = new Map(items.map((i) => [i.name, i] as const));
  const layer = new Map<string, number>();
  const visiting = new Set<string>();
  const depth = (name: string): number => {
    const known = layer.get(name);
    if (known !== undefined) return known;
    if (visiting.has(name)) return 0;
    visiting.add(name);
    const deps = (byName.get(name)?.deps ?? []).filter((d) => byName.has(d.name));
    const l = deps.length ? 1 + Math.max(...deps.map((d) => depth(d.name))) : 0;
    visiting.delete(name);
    layer.set(name, l);
    return l;
  };
  items.forEach((i) => depth(i.name));
  const rowsPerLayer = new Map<number, number>();
  const nodes = [...items]
    .sort((a, b) => layer.get(a.name)! - layer.get(b.name)! || a.name.localeCompare(b.name))
    .map((i) => {
      const l = layer.get(i.name)!;
      const row = rowsPerLayer.get(l) ?? 0;
      rowsPerLayer.set(l, row + 1);
      return { name: i.name, layer: l, row };
    });
  const edges = items.flatMap((i) =>
    i.deps.filter((d) => byName.has(d.name)).map((d) => ({ from: d.name, to: i.name, condition: d.condition })),
  );
  return {
    nodes,
    edges,
    layers: Math.max(0, ...nodes.map((n) => n.layer)) + 1,
    rows: Math.max(1, ...rowsPerLayer.values()),
  };
}
