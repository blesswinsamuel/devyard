import { For, Show } from "solid-js";
import { cn } from "~/lib/utils";
import { laneColor, type CommitGraphInfo } from "./graph";

/** Horizontal space per lane, in px. */
export const LANE_W = 14;
const WORKDIR_COLOR = "var(--warning)";

/**
 * One row's slice of the commit graph. The svg uses a unit-height viewBox
 * stretched to the row (preserveAspectRatio="none" with non-scaling strokes),
 * so segments meet the rows above and below exactly; the node is a plain
 * element so it stays round.
 */
export function GraphCell(props: { info: CommitGraphInfo | undefined; columns: number; selected: boolean; workdir: boolean }) {
  const width = () => Math.max(1, props.columns) * LANE_W;
  const x = (col: number) => col * LANE_W + LANE_W / 2;
  const color = (colorIndex: number) => (props.workdir ? WORKDIR_COLOR : laneColor(colorIndex));
  const stroke = {
    "stroke-width": "1.75",
    "stroke-linecap": "round",
    "vector-effect": "non-scaling-stroke",
    fill: "none",
  } as const;

  return (
    <div class="relative shrink-0 self-stretch" style={{ width: `${width()}px` }} aria-hidden="true">
      <svg viewBox={`0 0 ${width()} 1`} preserveAspectRatio="none" class="pointer-events-none absolute inset-0 size-full">
        <Show when={props.info?.incoming}>
          <line
            x1={x(props.info!.column)}
            y1={0}
            x2={x(props.info!.column)}
            y2={0.5}
            stroke={laneColor(props.info!.colorIndex)}
            {...stroke}
          />
        </Show>
        <For each={props.info?.edges ?? []}>
          {(edge) =>
            edge.type === "pass" ? (
              <line x1={x(edge.from)} y1={0} x2={x(edge.from)} y2={1} stroke={laneColor(edge.colorIndex)} {...stroke} />
            ) : edge.from === edge.to ? (
              <line x1={x(edge.from)} y1={0.5} x2={x(edge.to)} y2={1} stroke={color(edge.colorIndex)} {...stroke} />
            ) : (
              <path
                d={`M ${x(edge.from)} 0.5 C ${x(edge.from)} 0.85, ${x(edge.to)} 0.8, ${x(edge.to)} 1`}
                stroke={color(edge.colorIndex)}
                {...stroke}
              />
            )
          }
        </For>
      </svg>
      <Show when={props.info}>
        {(info) => (
          <span
            class={cn(
              "absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2",
              props.selected ? "size-3" : "size-2.5",
              props.workdir ? "border-warning bg-card" : "border-card",
            )}
            style={{
              left: `${x(info().column)}px`,
              "background-color": props.workdir ? undefined : laneColor(info().colorIndex),
            }}
          />
        )}
      </Show>
    </div>
  );
}
