import { createMemo, For, Show } from "solid-js";
import { A, useParams } from "@solidjs/router";
import { FolderX, Pin, TriangleAlert } from "lucide-solid";
import { Alert, AlertDescription, AlertTitle } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { ActionButton, ActionsDropdown } from "~/components/actions";
import { CopyButton } from "~/components/copy-button";
import { EmptyState } from "~/components/empty-state";
import { Meta, Page, PageHeader, PageSkeleton, Section, usePageTitle } from "~/components/page";
import { StatusBadge } from "~/components/status";
import { dockActions } from "~/data/dock";
import { entities, getProject, servicesOf, tasksOf } from "~/data/entities";
import { useProjectStats } from "~/data/stats";
import { paths } from "~/lib/paths";
import { isServiceFailing, projectTone } from "~/lib/status";
import { LogViewer } from "~/features/logs/log-viewer";
import { ConfigDriftBanner } from "./config-drift";
import { DependencyGraph } from "./dep-graph";
import { ServicesTable } from "./services-table";
import { TasksList } from "./tasks-list";

const HEADER_ACTIONS = [
  "project.start",
  "project.stop",
  "project.restart",
  "project.reload",
  "project.rebuild",
  "project.terminal",
  "project.git",
];

export function ProjectNotFound(props: { id: string }) {
  return (
    <EmptyState
      icon={FolderX}
      title={`No project named “${props.id}”`}
      description="It may have been removed, or it was never registered with this daemon."
      class="h-full"
    >
      <Button as={A} href={paths.home()} variant="outline">
        All projects
      </Button>
    </EmptyState>
  );
}

export function ProjectPage() {
  const params = useParams<{ project: string }>();
  const id = () => params.project;
  const project = () => getProject(id());
  const services = () => servicesOf(id());
  const tasks = () => tasksOf(id());
  const stats = useProjectStats(() => (project() ? id() : undefined));
  const failing = createMemo(() => services().filter(isServiceFailing).length);
  const hasDeps = () => services().some((s) => s.spec.dependsOn.length > 0);
  const target = () => ({ kind: "project" as const, project: id() });
  usePageTitle(() => id());

  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Show when={project()} fallback={<ProjectNotFound id={id()} />}>
        {(p) => (
          <Page>
            <PageHeader
              title={p().id}
              badges={
                <>
                  <StatusBadge tone={projectTone(p())} status={p().status} />
                  <Show when={p().desired === "partial"}>
                    <span class="text-2xs text-muted-foreground">partial: some services selected</span>
                  </Show>
                  <Show when={failing()}>
                    <span class="text-ui font-medium text-destructive">{failing()} failing</span>
                  </Show>
                </>
              }
              meta={
                <>
                  <Meta label="config">
                    <span class="font-mono text-2xs" title={p().configPath}>
                      {p().configPath}
                    </span>
                    <CopyButton text={p().configPath} label="Copy config path" />
                  </Meta>
                  <Show when={p().envFiles.length}>
                    <Meta label="env">
                      <span class="font-mono text-2xs" title={p().envFiles.join("\n")}>
                        {p().envFiles.map((f) => f.split("/").pop()).join(", ")}
                      </span>
                    </Meta>
                  </Show>
                  <Show when={p().links.length}>
                    <Meta label="links">
                      <span class="flex flex-wrap gap-2">
                        <For each={p().links}>
                          {(l) => (
                            <a href={l.url} target="_blank" rel="noreferrer" class="focus-ring rounded-sm text-ui hover:underline">
                              {l.name}
                            </a>
                          )}
                        </For>
                      </span>
                    </Meta>
                  </Show>
                  <Show when={p().hasConfig}>
                    <Meta label="services">
                      <span class="tabular">
                        {p().servicesRunning}/{p().servicesTotal} running
                      </span>
                    </Meta>
                  </Show>
                </>
              }
              actions={
                <>
                  <For each={HEADER_ACTIONS}>{(a) => <ActionButton id={a} target={target()} />}</For>
                  <ActionsDropdown target={target()} exclude={[...HEADER_ACTIONS, "project.open"]} />
                </>
              }
            >
              <Show when={p().error}>
                <Alert variant="destructive">
                  <TriangleAlert />
                  <AlertTitle>This project can't be loaded</AlertTitle>
                  <AlertDescription>
                    <p class="font-mono text-2xs break-all">{p().error}</p>
                    <p class="mt-1">Fix the config, then reload it. The project stays registered meanwhile.</p>
                  </AlertDescription>
                </Alert>
              </Show>
              <ConfigDriftBanner project={p()} />
            </PageHeader>

            <Section title="Services" count={services().length || undefined} id="services-heading">
              <Show
                when={services().length}
                fallback={
                  <p class="rounded-lg border border-dashed p-6 text-center text-ui text-muted-foreground">
                    <Show when={p().hasConfig} fallback={<>No <code class="font-mono">devyard.yml</code> here: this project has the git view and terminals. Create one to run services; it is picked up automatically.</>}>
                      No services defined.
                    </Show>
                  </p>
                }
              >
                <ServicesTable services={services()} stats={stats} />
              </Show>
            </Section>

            <Show when={tasks().length || hasDeps()}>
              <div class="grid gap-6 lg:grid-cols-2">
                <Show when={tasks().length}>
                  <Section title="Tasks" count={tasks().length} id="tasks-heading">
                    <TasksList tasks={tasks()} />
                  </Section>
                </Show>
                <Show when={hasDeps()}>
                  <Section title="Dependencies" id="deps-heading">
                    <DependencyGraph services={services()} />
                  </Section>
                </Show>
              </div>
            </Show>

            <Show when={services().length}>
              <Section title="Logs" id="logs-heading">
                <LogViewer
                  class="h-128"
                  label={`${p().id} logs`}
                  project={p().id}
                  sources={[]}
                  chipSources={services().map((s) => ({ kind: "service" as const, name: s.name }))}
                  emptyText="No service output yet."
                  toolbarEnd={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label="Pin logs to dock"
                      title="Pin to dock"
                      onClick={() => dockActions.pinLogs(p().id, [], `${p().id} · all`)}
                    >
                      <Pin />
                    </Button>
                  }
                />
              </Section>
            </Show>
          </Page>
        )}
      </Show>
    </Show>
  );
}
