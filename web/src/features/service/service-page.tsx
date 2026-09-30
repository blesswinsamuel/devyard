import { createSignal, For, Match, Show, Switch } from "solid-js";
import { A, useParams, useSearchParams } from "@solidjs/router";
import { Pin, Plug, ServerOff } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "~/components/ui/tabs";
import { ActionButton, ActionsDropdown } from "~/components/actions";
import { EmptyState } from "~/components/empty-state";
import { Meta, Page, PageHeader, PageSkeleton, usePageTitle } from "~/components/page";
import { HealthIndicator, StatusBadge } from "~/components/status";
import { UrlLinks } from "~/components/url-links";
import { dockActions } from "~/data/dock";
import { entities, getProject, getService } from "~/data/entities";
import { useProjectStats } from "~/data/stats";
import { now } from "~/lib/format";
import { paths } from "~/lib/paths";
import { serviceLabel, serviceTone } from "~/lib/status";
import { LogViewer } from "~/features/logs/log-viewer";
import { RunSelect } from "~/features/logs/run-select";
import { ProjectNotFound } from "~/features/project/project-page";
import { uptime } from "~/features/project/services-table";
import { SessionView } from "~/features/terminal/lazy";
import { ServiceDetails } from "./service-details";
import { ServiceMetrics } from "./service-metrics";

const HEADER_ACTIONS = ["service.start", "service.stop", "service.restart", "service.rebuild", "service.attach", "service.open-url"];
const TABS = ["logs", "details", "metrics", "attach"] as const;
type Tab = (typeof TABS)[number];

export function ServicePage() {
  const params = useParams<{ project: string; service: string }>();
  const [search, setSearch] = useSearchParams<{ tab?: string }>();
  const svc = () => getService(params.project, params.service);
  const stats = useProjectStats(() => (svc() ? params.project : undefined));
  const [runOffset, setRunOffset] = createSignal(0);
  const target = () => ({ kind: "service" as const, project: params.project, name: params.service });
  const tab = (): Tab => {
    const t = search.tab as Tab;
    if (t === "attach" && !svc()?.spec.tty) return "logs";
    return TABS.includes(t) ? t : "logs";
  };
  usePageTitle(() => `${params.service} · ${params.project}`);

  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Show when={getProject(params.project)} fallback={<ProjectNotFound id={params.project} />}>
        <Show
          when={svc()}
          fallback={
            <EmptyState icon={ServerOff} title={`No service “${params.service}” in ${params.project}`} class="h-full">
              <Button as={A} href={paths.project(params.project)} variant="outline">
                Back to {params.project}
              </Button>
            </EmptyState>
          }
        >
          {(s) => (
            <Page class="h-full min-h-0">
              <PageHeader
                title={s().name}
                badges={
                  <>
                    <StatusBadge tone={serviceTone(s())} status={s().status} label={serviceLabel(s())} />
                    <HealthIndicator health={s().health} detail={s().healthDetail} showLabel />
                  </>
                }
                meta={
                  <>
                    <Show when={s().message}>
                      <span class="text-foreground">{s().message}</span>
                    </Show>
                    <Meta label="uptime">
                      <span class="tabular">{uptime(s(), now())}</span>
                    </Meta>
                    <Meta label="restarts">
                      <span class="tabular">{s().restarts}</span>
                    </Meta>
                    <Show when={s().pid}>
                      <Meta label="pid">
                        <span class="tabular">{s().pid}</span>
                      </Meta>
                    </Show>
                    <Show when={s().urls.length}>
                      <UrlLinks urls={s().urls} />
                    </Show>
                  </>
                }
                actions={
                  <>
                    <For each={HEADER_ACTIONS}>{(a) => <ActionButton id={a} target={target()} />}</For>
                    <ActionsDropdown target={target()} inherit exclude={[...HEADER_ACTIONS, "service.logs"]} />
                  </>
                }
              />
              <Tabs value={tab()} onChange={(v: string) => setSearch({ tab: v === "logs" ? undefined : v })} class="min-h-0 flex-1 gap-3">
                <TabsList variant="line">
                  <TabsTrigger value="logs">Logs</TabsTrigger>
                  <TabsTrigger value="details">Details</TabsTrigger>
                  <TabsTrigger value="metrics">Metrics</TabsTrigger>
                  <Show when={s().spec.tty}>
                    <TabsTrigger value="attach">Attach</TabsTrigger>
                  </Show>
                </TabsList>
                <TabsContent value="logs" class="flex min-h-0 flex-col">
                  <LogViewer
                    class="min-h-96 flex-1"
                    label={`${s().name} logs`}
                    project={s().project}
                    sources={[{ kind: "service", name: s().name }]}
                    runOffset={runOffset()}
                    emptyText={s().run ? "No output in this run." : "This service hasn't run yet."}
                    toolbarStart={<RunSelect run={s().run} value={runOffset()} onChange={setRunOffset} />}
                    toolbarEnd={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Pin logs to dock"
                        title="Pin to dock"
                        onClick={() => dockActions.pinLogs(s().project, [{ kind: "service", name: s().name }], s().name)}
                      >
                        <Pin />
                      </Button>
                    }
                  />
                </TabsContent>
                <TabsContent value="details">
                  <ServiceDetails service={s()} />
                </TabsContent>
                <TabsContent value="metrics">
                  <ServiceMetrics service={s()} stats={stats} />
                </TabsContent>
                <TabsContent value="attach" class="flex min-h-0 flex-col">
                  <Switch>
                    <Match when={s().status === "running"}>
                      <div class="flex min-h-96 flex-1 flex-col overflow-hidden rounded-lg border">
                        <div class="flex items-center gap-2 border-b bg-card px-3 py-1.5 text-2xs text-muted-foreground">
                          <Plug class="size-3.5" /> Attached to run #{s().run}. Closing this view detaches; the service keeps running.
                          <Button size="xs" variant="ghost" class="ml-auto" onClick={() => dockActions.attach("service", s().project, s().name)}>
                            Open in dock
                          </Button>
                        </div>
                        <Show when={s().run} keyed>
                          <SessionView class="min-h-0 flex-1" target={{ kind: "service", project: s().project, name: s().name }} />
                        </Show>
                      </div>
                    </Match>
                    <Match when={true}>
                      <EmptyState icon={Plug} title="Not running" description="Start the service to attach to its terminal." class="rounded-lg border border-dashed py-12">
                        <ActionButton id="service.start" target={target()} variant="default" />
                      </EmptyState>
                    </Match>
                  </Switch>
                </TabsContent>
              </Tabs>
            </Page>
          )}
        </Show>
      </Show>
    </Show>
  );
}
