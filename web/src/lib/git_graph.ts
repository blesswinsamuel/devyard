import type { GitCommit } from "../types";

export interface GraphConnection {
  fromColumn: number;
  toColumn: number;
  colorIndex: number;
  type: "straight" | "merge" | "fork";
}

export interface CommitGraphInfo {
  column: number;
  colorIndex: number;
  connections: GraphConnection[];
  activeCount: number;
}

export const GRAPH_COLORS = [
  "#3b82f6", // blue
  "#10b981", // emerald
  "#8b5cf6", // violet
  "#f59e0b", // amber
  "#ec4899", // pink
  "#06b6d4", // cyan
  "#f97316", // orange
  "#84cc16", // lime
];

/**
 * Computes DAG column positions and connection paths for each commit in a top-down list.
 */
export function computeGitGraph(
  commits: GitCommit[]
): Map<string, CommitGraphInfo> {
  const result = new Map<string, CommitGraphInfo>();

  let activeColumns: (string | null)[] = [];
  const colorMap = new Map<string, number>();
  let nextColorIndex = 0;

  function getColor(hash: string): number {
    if (!colorMap.has(hash)) {
      colorMap.set(hash, nextColorIndex % GRAPH_COLORS.length);
      nextColorIndex++;
    }
    return colorMap.get(hash)!;
  }

  for (let i = 0; i < commits.length; i++) {
    const c = commits[i];
    const hash = c.hash;
    const parents = c.parents ?? [];

    let col = activeColumns.indexOf(hash);
    if (col === -1) {
      col = activeColumns.indexOf(null);
      if (col === -1) {
        col = activeColumns.length;
        activeColumns.push(hash);
      } else {
        activeColumns[col] = hash;
      }
    }

    const commitColor = getColor(hash);
    const connections: GraphConnection[] = [];

    if (parents.length === 0) {
      activeColumns[col] = null;
    } else {
      const firstParent = parents[0];
      activeColumns[col] = firstParent;
      if (!colorMap.has(firstParent)) {
        colorMap.set(firstParent, commitColor);
      }

      connections.push({
        fromColumn: col,
        toColumn: col,
        colorIndex: commitColor,
        type: "straight",
      });

      for (let pIdx = 1; pIdx < parents.length; pIdx++) {
        const parentHash = parents[pIdx];
        let pCol = activeColumns.indexOf(parentHash);
        if (pCol === -1) {
          pCol = activeColumns.indexOf(null);
          if (pCol === -1) {
            pCol = activeColumns.length;
            activeColumns.push(parentHash);
          } else {
            activeColumns[pCol] = parentHash;
          }
        }
        const pColor = getColor(parentHash);
        connections.push({
          fromColumn: col,
          toColumn: pCol,
          colorIndex: pColor,
          type: "merge",
        });
      }
    }

    while (
      activeColumns.length > 0 &&
      activeColumns[activeColumns.length - 1] === null
    ) {
      activeColumns.pop();
    }

    result.set(hash, {
      column: col,
      colorIndex: commitColor,
      connections,
      activeCount: Math.max(1, activeColumns.length, col + 1),
    });
  }

  return result;
}
