import { For, Show } from "solid-js";
import { GRAPH_COLORS, type CommitGraphInfo } from "~/lib/git_graph";

export const GIT_COL_WIDTH = 13;
const WORKDIR_COLOR = "#fbbf24"; // amber

function laneColor(colorIndex: number, workdir: boolean): string {
  return workdir
    ? WORKDIR_COLOR
    : GRAPH_COLORS[colorIndex % GRAPH_COLORS.length];
}

/**
 * One commit-list row's DAG strip. The svg uses a unit-height viewBox with
 * preserveAspectRatio="none" and vector-effect="non-scaling-stroke", so edges
 * span the full row height no matter how tall the row renders (Sublime Merge
 * style connectivity without measuring row heights). The node is a plain div
 * so it stays a perfect circle.
 */
export function CommitGraphCell(props: {
  info: CommitGraphInfo | undefined;
  columns: number;
  selected: boolean;
  workdir: boolean;
}) {
  const width = () => Math.max(1, props.columns) * GIT_COL_WIDTH;
  const vFrom = (edge: { from: number; to: number }) => edge.from === edge.to;
  const nodeRadius = () => (props.selected ? 4.5 : 3.5);

  return (
    <div
      class="relative self-stretch shrink-0"
      style={{ width: `${width()}px` }}
    >
      <svg
        viewBox={`0 0 ${width()} 1`}
        preserveAspectRatio="none"
        aria-hidden="true"
        class="pointer-events-none absolute inset-0 h-full w-full"
      >
        <For each={props.info?.edges ?? []}>
          {(edge) => {
            const x1 = edge.from * GIT_COL_WIDTH + GIT_COL_WIDTH / 2;
            const x2 = edge.to * GIT_COL_WIDTH + GIT_COL_WIDTH / 2;
            const color = laneColor(edge.colorIndex, props.workdir);
            if (vFrom(edge)) {
              return (
                <line
                  x1={x1}
                  y1={0}
                  x2={x2}
                  y2={1}
                  stroke={color}
                  stroke-width="1.75"
                  stroke-linecap="round"
                  vector-effect="non-scaling-stroke"
                />
              );
            }
            return (
              <path
                d={`M ${x1} 0.5 C ${x1} 0.75, ${x2} 0.75, ${x2} 1`}
                fill="none"
                stroke={color}
                stroke-width="1.75"
                stroke-linecap="round"
                vector-effect="non-scaling-stroke"
              />
            );
          }}
        </For>
      </svg>
      <Show when={props.info} keyed>
        {(info) => {
          const infoColor = () => laneColor(info.colorIndex, props.workdir);
          return (
            <span
              aria-hidden="true"
              class="absolute left-[0px] top-1/2 rounded-full border-[1.5px] border-background"
              style={{
                width: `${nodeRadius() * 2}px`,
                height: `${nodeRadius() * 2}px`,
                left: `${info.column * GIT_COL_WIDTH + GIT_COL_WIDTH / 2 - nodeRadius()}px`,
                "background-color": infoColor(),
                transform: "translateY(-50%)",
              }}
            />
          );
        }}
      </Show>
    </div>
  );
}
