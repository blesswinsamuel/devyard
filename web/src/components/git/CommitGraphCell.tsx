import { For, Show } from "solid-js";
import { GRAPH_COLORS, type CommitGraphInfo } from "~/lib/git_graph";

export const GIT_ROW_HEIGHT = 46;
export const GIT_COL_WIDTH = 13;
const WORKDIR_COLOR = "#fbbf24"; // amber

function laneColor(colorIndex: number, workdir: boolean): string {
  return workdir
    ? WORKDIR_COLOR
    : GRAPH_COLORS[colorIndex % GRAPH_COLORS.length];
}

/**
 * One commit-list row's DAG strip: vertical lane segments (pass-through and
 * parent edges) plus the commit node. Edges render first so the node sits on
 * top; the workdir pseudo-commit keeps its amber color.
 */
export function CommitGraphCell(props: {
  info: CommitGraphInfo | undefined;
  columns: number;
  selected: boolean;
  workdir: boolean;
}) {
  const width = () => Math.max(1, props.columns) * GIT_COL_WIDTH;

  return (
    <div
      class="relative h-[46px] shrink-0 self-stretch"
      style={{ width: `${width()}px` }}
    >
      <svg aria-hidden="true" class="pointer-events-none absolute inset-0 h-full w-full">
        <For each={props.info?.edges ?? []}>
          {(edge) => {
            const x1 = edge.from * GIT_COL_WIDTH + GIT_COL_WIDTH / 2;
            const x2 = edge.to * GIT_COL_WIDTH + GIT_COL_WIDTH / 2;
            const color = laneColor(edge.colorIndex, props.workdir);
            if (edge.from === edge.to) {
              return (
                <line
                  x1={x1}
                  y1={0}
                  x2={x2}
                  y2={GIT_ROW_HEIGHT}
                  stroke={color}
                  stroke-width="1.75"
                  stroke-linecap="round"
                />
              );
            }
            const y1 = GIT_ROW_HEIGHT / 2;
            const y2 = GIT_ROW_HEIGHT;
            return (
              <path
                d={`M ${x1} ${y1} C ${x1} ${(y1 + y2) / 2}, ${x2} ${(y1 + y2) / 2}, ${x2} ${y2}`}
                fill="none"
                stroke={color}
                stroke-width="1.75"
                stroke-linecap="round"
              />
            );
          }}
        </For>
        <Show when={props.info} keyed>
          {(info) => (
            <circle
              cx={info.column * GIT_COL_WIDTH + GIT_COL_WIDTH / 2}
              cy={GIT_ROW_HEIGHT / 2}
              r={props.selected ? 4.5 : 3.5}
              fill={laneColor(info.colorIndex, props.workdir)}
              stroke="var(--background)"
              stroke-width="1.5"
            />
          )}
        </Show>
      </svg>
    </div>
  );
}
