import type { GitCommit } from "~/lib/types";

export interface GraphEdge {
  from: number;
  to: number;
  colorIndex: number;
}

export interface CommitGraphInfo {
  column: number;
  colorIndex: number;
  edges: GraphEdge[];
  activeWidth: number;
}

export const GRAPH_COLORS = [
  "#818cf8", // indigo
  "#34d399", // emerald
  "#fbbf24", // amber
  "#f472b6", // pink
  "#38bdf8", // sky
  "#a78bfa", // violet
  "#fb923c", // orange
  "#2dd4bf", // teal
];

type Lane = { hash: string; colorIndex: number } | null;

/**
 * Computes DAG column positions and edges for a newest-first commit list.
 *
 * Each row resolves its own column, emits pass-through segments for every
 * other open lane (so lanes stay visually connected through unrelated rows),
 * resolves its parents' columns (claiming or reusing lanes, never duplicating
 * a lane), and records colors per lane so each branch keeps a stable color.
 */
export function computeGitGraph(commits: GitCommit[]): Map<string, CommitGraphInfo> {
  const result = new Map<string, CommitGraphInfo>();

  let lanes: Lane[] = [];
  const colorMap = new Map<string, number>();
  let nextColorIndex = 0;

  const laneIndexOf = (hash: string) => lanes.findIndex((l) => l?.hash === hash);

  const colorOf = (hash: string, inherit?: number): number => {
    let idx = colorMap.get(hash);
    if (idx === undefined) {
      idx = inherit !== undefined ? inherit : nextColorIndex++ % GRAPH_COLORS.length;
      colorMap.set(hash, idx);
    }
    return idx;
  };

  const trimTrailingNulls = () => {
    while (lanes.length > 0 && lanes[lanes.length - 1] === null) lanes.pop();
  };

  for (const commit of commits) {
    const hash = commit.hash;
    const parents = commit.parents ?? [];

    // 1. Resolve the commit's own column (claiming a lane if the commit is a
    //    tip of a new lane).
    let col = laneIndexOf(hash);
    if (col === -1) {
      col = lanes.findIndex((l) => l === null);
      if (col === -1) col = lanes.length;
      lanes[col] = { hash, colorIndex: colorOf(hash) };
    }
    const commitColor = lanes[col]!.colorIndex;

    // 2. Emit pass-through segments for every other open lane so lanes stay
    //    visually connected through rows that don't belong to them.
    const edges: GraphEdge[] = [];
    for (let i = 0; i < lanes.length; i++) {
      if (i !== col && lanes[i]) {
        edges.push({ from: i, to: i, colorIndex: lanes[i]!.colorIndex });
      }
    }

    // 3. Resolve parents. The first parent inherits the commit's lane color;
    //    merged parents get fresh colors. Ties into already-tracked lanes emit
    //    cross-column edges instead of duplicating lanes.
    if (parents.length === 0) {
      lanes[col] = null;
    } else {
      for (let pIdx = 0; pIdx < parents.length; pIdx++) {
        const parent = parents[pIdx]!;
        const isFirst = pIdx === 0;
        let pCol = laneIndexOf(parent);

        if (pCol === -1) {
          if (isFirst) {
            // First parent continues in the same lane.
            lanes[col] = { hash: parent, colorIndex: colorOf(parent, commitColor) };
            pCol = col;
          } else {
            pCol = lanes.findIndex((l) => l === null);
            if (pCol === -1) pCol = lanes.length;
            lanes[pCol] = { hash: parent, colorIndex: colorOf(parent) };
          }
        } else if (isFirst && pCol !== col) {
          // Parent already tracked elsewhere — close this lane rather than
          // duplicating the same hash in two columns.
          lanes[col] = null;
        }

        const edgeColor = isFirst ? colorOf(parent, commitColor) : colorOf(parent);
        edges.push({ from: col, to: pCol, colorIndex: edgeColor });
      }
    }

    trimTrailingNulls();

    result.set(hash, {
      column: col,
      colorIndex: commitColor,
      edges,
      activeWidth: Math.max(col + 1, lanes.length, 1),
    });
  }

  return result;
}
