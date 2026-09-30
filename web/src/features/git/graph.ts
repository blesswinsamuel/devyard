/** Minimal commit shape the lane layout needs (newest first). */
export interface GraphCommit {
  hash: string;
  parents: readonly string[];
}

/**
 * One segment drawn in a commit's row.
 * - `pass`: a lane that runs through the row untouched (full height).
 * - `parent`: from the commit's node (row middle) down to a parent's lane at
 *   the bottom of the row; straight when the columns match, curved otherwise.
 */
export interface GraphEdge {
  type: "pass" | "parent";
  from: number;
  to: number;
  colorIndex: number;
}

export interface CommitGraphInfo {
  column: number;
  colorIndex: number;
  /** A lane enters the node from above (false for branch tips). */
  incoming: boolean;
  edges: GraphEdge[];
  /** Lanes in use at this row; the widest row sizes the graph column. */
  activeWidth: number;
}

/** Lane colors cycle through the `--source-N` palette. */
export const LANE_COLORS = 8;

export const laneColor = (colorIndex: number) => `var(--source-${(colorIndex % LANE_COLORS) + 1})`;

type Lane = { hash: string; colorIndex: number } | null;

/**
 * Computes lane positions and edges for a newest-first commit list.
 *
 * Each row resolves its own column (claiming a free lane for branch tips),
 * emits pass-through segments for every other open lane, then routes its
 * parents: the first parent continues the commit's lane and color, further
 * parents (merges) claim a free lane with a fresh color, and a parent that is
 * already tracked in another lane is joined instead of duplicated.
 */
export function computeGitGraph(commits: readonly GraphCommit[]): Map<string, CommitGraphInfo> {
  const result = new Map<string, CommitGraphInfo>();
  const lanes: Lane[] = [];
  const colorMap = new Map<string, number>();
  let nextColor = 0;

  const laneOf = (hash: string) => lanes.findIndex((l) => l?.hash === hash);
  const freeLane = () => {
    const i = lanes.findIndex((l) => l === null);
    return i === -1 ? lanes.length : i;
  };
  const colorOf = (hash: string, inherit?: number): number => {
    let idx = colorMap.get(hash);
    if (idx === undefined) {
      idx = inherit ?? nextColor++ % LANE_COLORS;
      colorMap.set(hash, idx);
    }
    return idx;
  };

  for (const commit of commits) {
    let col = laneOf(commit.hash);
    const incoming = col !== -1;
    if (!incoming) {
      col = freeLane();
      lanes[col] = { hash: commit.hash, colorIndex: colorOf(commit.hash) };
    }
    const commitColor = lanes[col]!.colorIndex;

    const edges: GraphEdge[] = [];
    for (let i = 0; i < lanes.length; i++) {
      const lane = lanes[i];
      if (i !== col && lane) edges.push({ type: "pass", from: i, to: i, colorIndex: lane.colorIndex });
    }

    if (commit.parents.length === 0) lanes[col] = null;
    commit.parents.forEach((parent, idx) => {
      const first = idx === 0;
      let pCol = laneOf(parent);
      if (pCol === -1) {
        if (first) {
          pCol = col;
          lanes[col] = { hash: parent, colorIndex: colorOf(parent, commitColor) };
        } else {
          pCol = freeLane();
          lanes[pCol] = { hash: parent, colorIndex: colorOf(parent) };
        }
      } else if (first && pCol !== col) {
        // The parent already has a lane: join it and free this one.
        lanes[col] = null;
      }
      edges.push({ type: "parent", from: col, to: pCol, colorIndex: first ? colorOf(parent, commitColor) : colorOf(parent) });
    });

    while (lanes.length > 0 && lanes[lanes.length - 1] === null) lanes.pop();

    result.set(commit.hash, {
      column: col,
      colorIndex: commitColor,
      incoming,
      edges,
      activeWidth: Math.max(col + 1, lanes.length, 1),
    });
  }

  return result;
}

/** Widest row of a computed graph (at least one lane). */
export function graphWidth(graph: Map<string, CommitGraphInfo>): number {
  let max = 1;
  for (const info of graph.values()) if (info.activeWidth > max) max = info.activeWidth;
  return max;
}
