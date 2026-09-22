import { For, Show, createSignal, onMount, onCleanup } from "solid-js";
import { GRAPH_COLORS, type CommitGraphInfo } from "~/lib/git_graph";

export const GIT_COL_WIDTH = 13;
const WORKDIR_COLOR = "#fbbf24"; // amber
const DEFAULT_ROW_HEIGHT = 46;

function laneColor(colorIndex: number, workdir: boolean): string {
  return workdir
    ? WORKDIR_COLOR
    : GRAPH_COLORS[colorIndex % GRAPH_COLORS.length];
}

/**
 * One commit-list row's DAG strip. The row's rendered height is measured with
 * a ResizeObserver so lane segments span the full row and lines connect
 * continuously across rows (Sublime-Merge style), even when rows wrap.
 */
export function CommitGraphCell(props: {
  info: CommitGraphInfo | undefined;
  columns: number;
  selected: boolean;
  workdir: boolean;
}) {
  const width = () => Math.max(1, props.columns) * GIT_COL_WIDTH;
  const [rowHeight, setRowHeight] = createSignal(DEFAULT_ROW_HEIGHT);

  let el: HTMLDivElement | undefined;
  let ro: ResizeObserver | undefined;

  onMount(() => {
    if (!el) return;
    ro = new ResizeObserver(() => {
      const h = el!.offsetHeight;
      if (h > 0) setRowHeight(h);
    });
    ro.observe(el);
    setRowHeight(el.offsetHeight || DEFAULT_ROW_HEIGHT);
    onCleanup(() => ro?.disconnect());
  });

  const y1 = () => rowHeight() / 2;
  const y2 = () => rowHeight();

  return (
    <div
      ref={el}
      class="relative self-stretch shrink-0"
      style={{ width: `${width()}px` }}
    >
      <svg
        aria-hidden="true"
        width={width()}
        height={rowHeight()}
        viewBox={`0 0 ${width()} ${rowHeight()}`}
        class="pointer-events-none absolute inset-0"
      >
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
                  y2={y2()}
                  stroke={color}
                  stroke-width="1.75"
                  stroke-linecap="round"
                />
              );
            }
            return (
              <path
                d={`M ${x1} ${y1()} C ${x1} ${(y1() + y2()) / 2}, ${x2} ${(y1() + y2()) / 2}, ${x2} ${y2()}`}
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
              cy={y1()}
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
