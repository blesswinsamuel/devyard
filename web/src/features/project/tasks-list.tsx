import { For, Show } from "solid-js";
import { A } from "@solidjs/router";
import { ActionButton, ActionContextMenu, ActionsDropdown } from "~/components/actions";
import { StatusBadge } from "~/components/status";
import type { TaskEntity } from "~/data/entities";
import { formatDuration, formatRelative, now } from "~/lib/format";
import { paths } from "~/lib/paths";
import { taskLabel, taskTone } from "~/lib/status";
import { targetAttrs } from "~/app/runtime";

export function lastRunSummary(t: TaskEntity, nowMs: number): string {
  if (!t.run) return "never run";
  if (t.status === "running" || t.status === "stopping") return `running ${formatDuration(nowMs - t.startedAt)}`;
  const parts = [`#${t.run}`];
  if (t.finishedAt && t.startedAt) parts.push(`took ${formatDuration(t.finishedAt - t.startedAt)}`);
  if (t.finishedAt) parts.push(formatRelative(t.finishedAt, nowMs));
  return parts.join(" · ");
}

export function TasksList(props: { tasks: TaskEntity[] }) {
  return (
    <ul class="flex flex-col divide-y rounded-lg border bg-card">
      <For each={props.tasks}>
        {(t) => {
          const target = { kind: "task" as const, project: t.project, name: t.name };
          return (
            <ActionContextMenu
              as="li"
              target={target}
              class="focus-ring flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2"
              triggerProps={{ tabindex: 0, "aria-label": `Task ${t.name}, ${taskLabel(t)}`, ...targetAttrs(target) }}
            >
              <div class="flex min-w-0 flex-1 flex-col">
                <A href={paths.task(t.project, t.name)} class="focus-ring truncate rounded-sm text-ui font-medium hover:underline">
                  {t.name}
                </A>
                <span class="truncate font-mono text-2xs text-muted-foreground" title={t.spec.command}>
                  {t.spec.command}
                </span>
              </div>
              <div class="flex flex-col items-end gap-0.5">
                <StatusBadge tone={taskTone(t)} status={t.status} label={taskLabel(t)} />
                <span class="tabular text-2xs text-muted-foreground">{lastRunSummary(t, now())}</span>
              </div>
              <div class="flex items-center gap-0.5">
                <ActionButton id="task.run" target={target} iconOnly variant="ghost" size="icon-xs" />
                <ActionButton id="task.run-args" target={target} iconOnly variant="ghost" size="icon-xs" />
                <ActionButton id="task.stop" target={target} iconOnly variant="ghost" size="icon-xs" />
                <Show when={t.status === "running" && t.spec.tty}>
                  <ActionButton id="task.attach" target={target} iconOnly variant="ghost" size="icon-xs" />
                </Show>
                <ActionsDropdown target={target} exclude={["task.run", "task.run-args", "task.stop", "task.attach"]} size="icon-xs" />
              </div>
            </ActionContextMenu>
          );
        }}
      </For>
    </ul>
  );
}
