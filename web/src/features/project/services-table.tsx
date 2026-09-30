import { For, Show, type Accessor } from "solid-js";
import { A, useNavigate } from "@solidjs/router";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "~/components/ui/table";
import { ActionButton, ActionContextMenu, ActionsDropdown } from "~/components/actions";
import { Sparkline } from "~/components/sparkline";
import { HealthIndicator, StatusBadge } from "~/components/status";
import { UrlLinks } from "~/components/url-links";
import type { ServiceEntity } from "~/data/entities";
import { statKey, STATS_HISTORY, type ProjectStats } from "~/data/stats";
import { formatBytes, formatDuration, formatPercent, formatRelative, now } from "~/lib/format";
import { isMobile } from "~/lib/media";
import { paths } from "~/lib/paths";
import { serviceLabel, serviceTone } from "~/lib/status";
import { targetAttrs } from "~/app/runtime";

export function uptime(s: ServiceEntity, nowMs: number): string {
  if (s.status === "running" && s.startedAt) return formatDuration(nowMs - s.startedAt);
  if (s.finishedAt) return `${s.status === "exited" || s.status === "failed" ? "exited" : "stopped"} ${formatRelative(s.finishedAt, nowMs)}`;
  return "—";
}

const ROW_ACTIONS = ["service.start", "service.stop", "service.restart"];

function RowActions(props: { s: ServiceEntity }) {
  const target = () => ({ kind: "service" as const, project: props.s.project, name: props.s.name });
  return (
    <div class="flex items-center justify-end gap-0.5">
      <For each={ROW_ACTIONS}>{(id) => <ActionButton id={id} target={target()} iconOnly variant="ghost" size="icon-xs" />}</For>
      <ActionsDropdown target={target()} exclude={[...ROW_ACTIONS, "service.logs"]} size="icon-xs" />
    </div>
  );
}

function Metric(props: { s: ServiceEntity; stats: ProjectStats; kind: "cpu" | "rss" }) {
  const series = () => props.stats[statKey("service", props.s.name)];
  const value = () => {
    const l = series()?.latest;
    if (!l || props.s.status !== "running") return "—";
    return props.kind === "cpu" ? formatPercent(l.cpu) : formatBytes(l.rss);
  };
  return (
    <div class="flex items-center justify-end gap-2">
      <Sparkline
        class="hidden w-16 lg:block"
        values={series()?.[props.kind] ?? []}
        capacity={STATS_HISTORY}
        max={props.kind === "cpu" ? 100 : undefined}
        color={props.kind === "cpu" ? "var(--primary)" : "var(--info)"}
        label={`${props.kind === "cpu" ? "CPU" : "Memory"} history for ${props.s.name}`}
      />
      <span class="tabular w-16 text-right">{value()}</span>
    </div>
  );
}

function StatusCell(props: { s: ServiceEntity }) {
  return (
    <div class="flex min-w-0 flex-col items-start gap-0.5">
      <StatusBadge tone={serviceTone(props.s)} status={props.s.status} label={serviceLabel(props.s)} />
      <Show when={props.s.message}>
        <span class="max-w-56 truncate text-2xs text-muted-foreground" title={props.s.message}>
          {props.s.message}
        </span>
      </Show>
    </div>
  );
}

export function ServicesTable(props: { services: ServiceEntity[]; stats: Accessor<ProjectStats> }) {
  const navigate = useNavigate();
  return (
    <Show when={!isMobile()} fallback={<ServiceCards services={props.services} stats={props.stats} />}>
      <Table class="text-ui">
        <TableHeader>
          <TableRow>
            <TableHead>Service</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Health</TableHead>
            <TableHead class="text-right">Uptime</TableHead>
            <TableHead class="text-right">Restarts</TableHead>
            <TableHead class="text-right">PID</TableHead>
            <TableHead class="text-right">CPU</TableHead>
            <TableHead class="text-right">Memory</TableHead>
            <TableHead>URLs</TableHead>
            <TableHead>
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <For each={props.services}>
            {(s) => {
              const target = { kind: "service" as const, project: s.project, name: s.name };
              return (
                <ActionContextMenu
                  as={TableRow}
                  target={target}
                  class="focus-ring cursor-pointer"
                  triggerProps={{
                    tabindex: 0,
                    "aria-label": `Service ${s.name}, ${serviceLabel(s)}`,
                    ...targetAttrs(target),
                    onClick: (e: MouseEvent) => {
                      if (!(e.target as Element).closest("a,button")) navigate(paths.service(s.project, s.name));
                    },
                    onKeyDown: (e: KeyboardEvent) => {
                      if (e.key === "Enter" && e.target === e.currentTarget) navigate(paths.service(s.project, s.name));
                    },
                  }}
                >
                  <TableCell class="font-medium">
                    <A href={paths.service(s.project, s.name)} class="focus-ring rounded-sm hover:underline" tabindex="-1">
                      {s.name}
                    </A>
                  </TableCell>
                  <TableCell>
                    <StatusCell s={s} />
                  </TableCell>
                  <TableCell>
                    <Show when={s.health} fallback={<span class="text-muted-foreground">—</span>}>
                      <HealthIndicator health={s.health} detail={s.healthDetail} showLabel />
                    </Show>
                  </TableCell>
                  <TableCell class="tabular text-right whitespace-nowrap">{uptime(s, now())}</TableCell>
                  <TableCell class="tabular text-right">{s.restarts || <span class="text-muted-foreground">0</span>}</TableCell>
                  <TableCell class="tabular text-right text-muted-foreground">{s.pid || "—"}</TableCell>
                  <TableCell>
                    <Metric s={s} stats={props.stats()} kind="cpu" />
                  </TableCell>
                  <TableCell>
                    <Metric s={s} stats={props.stats()} kind="rss" />
                  </TableCell>
                  <TableCell class="max-w-56">
                    <UrlLinks urls={s.urls} max={1} />
                  </TableCell>
                  <TableCell>
                    <RowActions s={s} />
                  </TableCell>
                </ActionContextMenu>
              );
            }}
          </For>
        </TableBody>
      </Table>
    </Show>
  );
}

function ServiceCards(props: { services: ServiceEntity[]; stats: Accessor<ProjectStats> }) {
  return (
    <ul class="flex flex-col gap-2">
      <For each={props.services}>
        {(s) => (
          <li class="rounded-lg border bg-card p-3" {...targetAttrs({ kind: "service", project: s.project, name: s.name })}>
            <div class="flex items-start gap-2">
              <A href={paths.service(s.project, s.name)} class="focus-ring min-w-0 flex-1 truncate rounded-sm font-medium">
                {s.name}
              </A>
              <HealthIndicator health={s.health} detail={s.healthDetail} />
              <StatusCell s={s} />
            </div>
            <div class="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-ui">
              <span class="text-muted-foreground">Uptime</span>
              <span class="tabular text-right">{uptime(s, now())}</span>
              <span class="text-muted-foreground">CPU</span>
              <Metric s={s} stats={props.stats()} kind="cpu" />
              <span class="text-muted-foreground">Memory</span>
              <Metric s={s} stats={props.stats()} kind="rss" />
            </div>
            <Show when={s.urls.length}>
              <UrlLinks class="mt-2" urls={s.urls} />
            </Show>
            <div class="mt-2 border-t pt-2">
              <RowActions s={s} />
            </div>
          </li>
        )}
      </For>
    </ul>
  );
}
