import { createMemo, For } from "solid-js";
import { useNavigate } from "@solidjs/router";
import type { ServiceEntity } from "~/data/entities";
import { paths } from "~/lib/paths";
import { serviceLabel, serviceTone, type Tone } from "~/lib/status";
import { layoutGraph } from "./graph-layout";

const NODE_W = 132;
const NODE_H = 30;
const GAP_X = 48;
const GAP_Y = 12;

const TONE_VAR: Record<Tone, string> = {
  success: "var(--success)",
  warning: "var(--warning)",
  danger: "var(--destructive)",
  muted: "var(--muted-foreground)",
  info: "var(--info)",
};

/** depends_on as a small left-to-right layered graph. Each edge means the
 * dependent waits until the dependency is ready. */
export function DependencyGraph(props: { services: ServiceEntity[] }) {
  const navigate = useNavigate();
  const graph = createMemo(() =>
    layoutGraph(props.services.map((s) => ({ name: s.name, deps: s.spec.dependsOn }))),
  );
  const pos = (name: string) => {
    const n = graph().nodes.find((x) => x.name === name)!;
    return { x: n.layer * (NODE_W + GAP_X), y: n.row * (NODE_H + GAP_Y) };
  };
  const width = () => graph().layers * (NODE_W + GAP_X) - GAP_X;
  const height = () => graph().rows * (NODE_H + GAP_Y) - GAP_Y;
  const svc = (name: string) => props.services.find((s) => s.name === name);

  return (
    <div class="overflow-x-auto rounded-lg border bg-card p-3">
      <svg
        width={width()}
        height={height()}
        viewBox={`-2 -2 ${width() + 4} ${height() + 4}`}
        role="group"
        aria-label="Service dependency graph"
        class="block text-ui"
      >
        <defs>
          <marker id="dep-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <path d="M0,0 L8,4 L0,8 z" fill="var(--muted-foreground)" />
          </marker>
        </defs>
        <For each={graph().edges}>
          {(e) => {
            const a = () => pos(e.from);
            const b = () => pos(e.to);
            const d = () => {
              const x1 = a().x + NODE_W;
              const y1 = a().y + NODE_H / 2;
              const x2 = b().x - 2;
              const y2 = b().y + NODE_H / 2;
              const mx = (x1 + x2) / 2;
              return `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`;
            };
            return (
              <path
                d={d()}
                fill="none"
                stroke="var(--muted-foreground)"
                stroke-opacity="0.6"
                stroke-width="1.25"
                marker-end="url(#dep-arrow)"
              >
                <title>
                  {e.to} waits for {e.from} to be ready
                </title>
              </path>
            );
          }}
        </For>
        <For each={graph().nodes}>
          {(n) => {
            const s = () => svc(n.name);
            const color = () => (s() ? TONE_VAR[serviceTone(s()!)] : "var(--muted-foreground)");
            const p = () => pos(n.name);
            return (
              <g
                transform={`translate(${p().x},${p().y})`}
                role="link"
                tabindex="0"
                class="cursor-pointer outline-none [&:focus-visible>rect]:stroke-(--ring)"
                aria-label={`${n.name}: ${s() ? serviceLabel(s()!) : "unknown"}`}
                onClick={() => s() && navigate(paths.service(s()!.project, n.name))}
                onKeyDown={(e) => e.key === "Enter" && s() && navigate(paths.service(s()!.project, n.name))}
              >
                <rect width={NODE_W} height={NODE_H} rx="6" fill="var(--background)" stroke="var(--border-strong)" stroke-width="1.5" />
                <circle cx="12" cy={NODE_H / 2} r="4" fill={color()} />
                <text x="22" y={NODE_H / 2} dominant-baseline="central" fill="var(--foreground)">
                  {n.name.length > 15 ? `${n.name.slice(0, 14)}…` : n.name}
                </text>
              </g>
            );
          }}
        </For>
      </svg>
    </div>
  );
}
