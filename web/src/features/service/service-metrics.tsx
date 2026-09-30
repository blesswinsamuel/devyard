import { Show, type Accessor } from "solid-js";
import { Sparkline } from "~/components/sparkline";
import type { ServiceEntity } from "~/data/entities";
import { statKey, STATS_HISTORY, STATS_INTERVAL_MS, type ProjectStats } from "~/data/stats";
import { formatBytes, formatDuration, formatPercent } from "~/lib/format";

function Chart(props: { title: string; value: string; peak: string; values: number[]; max?: number; color: string }) {
  return (
    <section class="flex flex-col gap-2 rounded-lg border bg-card p-4" aria-label={props.title}>
      <div class="flex items-baseline justify-between gap-2">
        <h3 class="text-sm font-semibold">{props.title}</h3>
        <span class="tabular text-xl font-semibold">{props.value}</span>
      </div>
      <Sparkline class="h-32 w-full" values={props.values} capacity={STATS_HISTORY} max={props.max} color={props.color} label={`${props.title} history`} />
      <div class="flex justify-between text-2xs text-muted-foreground">
        <span>−{formatDuration(STATS_HISTORY * STATS_INTERVAL_MS)}</span>
        <span>peak {props.peak}</span>
        <span>now</span>
      </div>
    </section>
  );
}

export function ServiceMetrics(props: { service: ServiceEntity; stats: Accessor<ProjectStats> }) {
  const series = () => props.stats()[statKey("service", props.service.name)];
  return (
    <Show
      when={series()}
      fallback={
        <p class="rounded-lg border border-dashed p-8 text-center text-ui text-muted-foreground">
          {props.service.status === "running" ? "Collecting samples…" : "No metrics while the service isn't running."}
        </p>
      }
    >
      {(s) => (
        <div class="flex flex-col gap-4">
          <div class="grid gap-4 md:grid-cols-2">
            <Chart
              title="CPU"
              value={formatPercent(s().latest.cpu)}
              peak={formatPercent(Math.max(0, ...s().cpu))}
              values={s().cpu}
              max={100}
              color="var(--primary)"
            />
            <Chart
              title="Memory (RSS)"
              value={formatBytes(s().latest.rss)}
              peak={formatBytes(Math.max(0, ...s().rss))}
              values={s().rss}
              color="var(--info)"
            />
          </div>
          <p class="text-ui text-muted-foreground">
            <span class="tabular">{s().latest.procs}</span> {s().latest.procs === 1 ? "process" : "processes"} in the group · pid{" "}
            <span class="tabular">{s().latest.pid || "—"}</span> · sampled every {formatDuration(STATS_INTERVAL_MS)}. CPU is
            the sum over the process group (100% = one core).
          </p>
        </div>
      )}
    </Show>
  );
}
