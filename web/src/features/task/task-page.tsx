import { createEffect, createSignal, For, Match, on, Show, Switch } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { ListX, Play } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { ActionButton, ActionsDropdown } from "~/components/actions";
import { EmptyState } from "~/components/empty-state";
import { Meta, Page, PageHeader, PageSkeleton, usePageTitle } from "~/components/page";
import { StatusBadge } from "~/components/status";
import { entities, getProject, getTask } from "~/data/entities";
import { joinArgs } from "~/lib/args";
import { now } from "~/lib/format";
import { paths } from "~/lib/paths";
import { taskLabel, taskTone } from "~/lib/status";
import { LogViewer } from "~/features/logs/log-viewer";
import { RunSelect } from "~/features/logs/run-select";
import { ProjectNotFound } from "~/features/project/project-page";
import { lastRunSummary } from "~/features/project/tasks-list";
import { SessionView } from "~/features/terminal/lazy";
import { StdinBar } from "~/features/terminal/stdin";

const HEADER_ACTIONS = ["task.run", "task.run-args", "task.stop", "task.kill", "task.attach"];

/**
 * Running + tty → live xterm session (keyboard goes to the task).
 * Running, no tty → log viewer + one-line stdin.
 * Otherwise → log viewer of the selected run.
 */
export function TaskPage() {
  const params = useParams<{ project: string; task: string }>();
  const task = () => getTask(params.project, params.task);
  const target = () => ({ kind: "task" as const, project: params.project, name: params.task });
  const [runOffset, setRunOffset] = createSignal(0);
  const active = () => task()?.status === "running" || task()?.status === "stopping";
  usePageTitle(() => `${params.task} · ${params.project}`);

  // A new run always shows live output.
  createEffect(on(() => task()?.run, () => setRunOffset(0), { defer: true }));

  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Show when={getProject(params.project)} fallback={<ProjectNotFound id={params.project} />}>
        <Show
          when={task()}
          fallback={
            <EmptyState icon={ListX} title={`No task “${params.task}” in ${params.project}`} class="h-full">
              <Button as={A} href={paths.project(params.project)} variant="outline">
                Back to {params.project}
              </Button>
            </EmptyState>
          }
        >
          {(t) => (
            <Page class="h-full min-h-0">
              <PageHeader
                title={t().name}
                badges={<StatusBadge tone={taskTone(t())} status={t().status} label={taskLabel(t())} />}
                meta={
                  <>
                    <Meta label="command">
                      <code class="font-mono text-2xs">{t().spec.command}</code>
                    </Meta>
                    <Show when={t().args.length}>
                      <Meta label="args">
                        <code class="font-mono text-2xs">{joinArgs(t().args)}</code>
                      </Meta>
                    </Show>
                    <span class="tabular">{lastRunSummary(t(), now())}</span>
                    <Show when={t().message}>
                      <span>{t().message}</span>
                    </Show>
                  </>
                }
                actions={
                  <>
                    <For each={HEADER_ACTIONS}>{(a) => <ActionButton id={a} target={target()} />}</For>
                    <ActionsDropdown target={target()} inherit exclude={[...HEADER_ACTIONS, "task.open"]} />
                  </>
                }
              />
              <Switch>
                <Match when={!t().run && !active()}>
                  <EmptyState
                    icon={Play}
                    title="This task hasn't run yet"
                    description={<code class="font-mono">{t().spec.command}</code>}
                    class="rounded-lg border border-dashed py-12"
                  >
                    <div class="flex gap-2">
                      <ActionButton id="task.run" target={target()} variant="default" />
                      <ActionButton id="task.run-args" target={target()} />
                    </div>
                  </EmptyState>
                </Match>
                <Match when={active() && t().spec.tty}>
                  <div class="flex min-h-96 flex-1 flex-col overflow-hidden rounded-lg border">
                    <Show when={t().run} keyed>
                      <SessionView
                        class="min-h-0 flex-1"
                        target={{ kind: "task", project: t().project, name: t().name }}
                        endActions={() => <ActionButton id="task.run" target={target()} label="Run again" />}
                      />
                    </Show>
                  </div>
                </Match>
                <Match when={true}>
                  <div class="flex min-h-96 flex-1 flex-col overflow-hidden rounded-lg border">
                    <LogViewer
                      class="min-h-0 flex-1 rounded-none border-0"
                      label={`${t().name} output`}
                      project={t().project}
                      sources={[{ kind: "task", name: t().name }]}
                      runOffset={runOffset()}
                      emptyText="No output in this run."
                      toolbarStart={<RunSelect run={t().run} value={runOffset()} onChange={setRunOffset} />}
                    />
                    <Show when={active() && !t().spec.tty}>
                      <Show when={t().run} keyed>
                        <StdinBar target={{ kind: "task", project: t().project, name: t().name }} />
                      </Show>
                    </Show>
                  </div>
                </Match>
              </Switch>
            </Page>
          )}
        </Show>
      </Show>
    </Show>
  );
}
