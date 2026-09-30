import { createMemo, Show } from "solid-js";
import { cn } from "~/lib/utils";

/**
 * Minimal SVG sparkline (line + soft area). The viewBox is normalized so the
 * element scales with its CSS box; `max` pins the y-range (e.g. 100 for CPU %).
 */
export function Sparkline(props: {
  values: number[];
  max?: number;
  class?: string;
  label: string;
  /** Colour token (defaults to the primary accent). */
  color?: string;
  capacity?: number;
}) {
  const W = 100;
  const H = 24;
  const path = createMemo(() => {
    const vals = props.values;
    if (vals.length < 2) return null;
    const cap = Math.max(props.capacity ?? vals.length, vals.length);
    const peak = Math.max(props.max ?? 0, ...vals, 1e-9);
    const step = W / (cap - 1);
    const offset = (cap - vals.length) * step;
    const pts = vals.map((v, i) => [offset + i * step, H - 1 - (Math.max(0, v) / peak) * (H - 2)] as const);
    const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(2)},${y.toFixed(2)}`).join("");
    const area = `${line}L${pts[pts.length - 1]![0].toFixed(2)},${H}L${pts[0]![0].toFixed(2)},${H}Z`;
    return { line, area };
  });
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      class={cn("block h-6 w-20 overflow-visible", props.class)}
      role="img"
      aria-label={props.label}
      style={{ color: props.color ?? "var(--primary)" }}
    >
      <Show when={path()} fallback={<line x1="0" x2={W} y1={H - 1} y2={H - 1} stroke="currentColor" stroke-opacity="0.25" />}>
        {(p) => (
          <>
            <path d={p().area} fill="currentColor" fill-opacity="0.12" />
            <path d={p().line} fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" />
          </>
        )}
      </Show>
    </svg>
  );
}
