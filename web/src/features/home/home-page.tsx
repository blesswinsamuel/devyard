import { createMemo, For, Match, Show, Switch } from "solid-js";
import { A, useNavigate } from "@solidjs/router";
import { Boxes, GitBranch, Plus, TriangleAlert } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "~/components/ui/table";
import { ActionButton, ActionContextMenu, ActionsDropdown } from "~/components/actions";
import { EmptyState } from "~/components/empty-state";
import { Page, PageHeader, PageSkeleton, usePageTitle } from "~/components/page";
import { StatusBadge } from "~/components/status";
import { UrlLinks } from "~/components/url-links";
import { entities, getGit, projectList, servicesOf, type ProjectEntity } from "~/data/entities";
import { isMobile } from "~/lib/media";
import { paths } from "~/lib/paths";
import { isServiceFailing, projectTone } from "~/lib/status";
import { cn } from "~/lib/utils";
import { targetAttrs } from "~/app/runtime";
import { setAddProjectOpen } from "~/app/ui-state";

function projectStats(id: string) {
  const services = servicesOf(id);
  const failing = services.filter(isServiceFailing).length;
  const withHealth = services.filter((s) => s.health !== "");
  const healthy = withHealth.filter((s) => s.health === "healthy").length;
  const urls = [...new Set(services.flatMap((s) => s.urls))];
  return { failing, healthy, checked: withHealth.length, urls };
}

function HealthSummary(props: { id: string }) {
  const st = createMemo(() => projectStats(props.id));
  return (
    <Switch fallback={<span class="text-muted-foreground">—</span>}>
      <Match when={st().failing > 0}>
        <span class="inline-flex items-center gap-1 font-medium text-destructive">
          <TriangleAlert class="size-3.5" /> {st().failing} failing
        </span>
      </Match>
      <Match when={st().checked > 0}>
        <span class={st().healthy === st().checked ? "text-success" : "text-warning"}>
          {st().healthy}/{st().checked} healthy
        </span>
      </Match>
    </Switch>
  );
}

function GitCell(props: { id: string }) {
  const g = () => getGit(props.id);
  return (
    <Show when={g()?.isRepo} fallback={<span class="text-muted-foreground">—</span>}>
      <span class="inline-flex min-w-0 items-center gap-1.5 whitespace-nowrap text-ui">
        <GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
        <span class="max-w-40 truncate">{g()!.branch || g()!.headHash.slice(0, 7)}</span>
        <Show when={g()!.ahead}>
          <span class="tabular text-muted-foreground">↑{g()!.ahead}</span>
        </Show>
        <Show when={g()!.behind}>
          <span class="tabular text-muted-foreground">↓{g()!.behind}</span>
        </Show>
        <Show when={!g()!.isClean}>
          <span class="tabular text-warning" title="uncommitted changes">
            ●{g()!.staged + g()!.dirty + g()!.untracked || ""}
          </span>
        </Show>
      </span>
    </Show>
  );
}

function PrimaryAction(props: { project: ProjectEntity }) {
  const target = () => ({ kind: "project" as const, project: props.project.id });
  return (
    <div class="flex items-center justify-end gap-1">
      <ActionButton id="project.start" target={target()} size="xs" />
      <ActionButton id="project.stop" target={target()} size="xs" />
      <ActionButton id="project.reload" target={target()} size="xs" iconOnly label="Reload config" />
      <ActionsDropdown target={target()} exclude={["project.start", "project.stop", "project.open"]} size="icon-xs" />
    </div>
  );
}

function ProjectsTable() {
  const navigate = useNavigate();
  return (
    <Table class="text-ui">
      <TableHeader>
        <TableRow>
          <TableHead>Project</TableHead>
          <TableHead>Status</TableHead>
          <TableHead class="text-right">Services</TableHead>
          <TableHead>Health</TableHead>
          <TableHead>Branch</TableHead>
          <TableHead class="hidden xl:table-cell">URLs</TableHead>
          <TableHead class="text-right">
            <span class="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <For each={projectList()}>
          {(p) => (
            <ActionContextMenu
              as={TableRow}
              target={{ kind: "project", project: p.id }}
              class="focus-ring cursor-pointer"
              triggerProps={{
                tabindex: 0,
                "aria-label": `Project ${p.id}`,
                ...targetAttrs({ kind: "project", project: p.id }),
                onClick: (e: MouseEvent) => {
                  if (!(e.target as Element).closest("a,button")) navigate(paths.project(p.id));
                },
                onKeyDown: (e: KeyboardEvent) => {
                  if (e.key === "Enter" && e.target === e.currentTarget) navigate(paths.project(p.id));
                },
              }}
            >
              <TableCell class="font-medium">
                <A href={paths.project(p.id)} class="focus-ring rounded-sm hover:underline" tabindex="-1">
                  {p.id}
                </A>
                <Show when={p.error}>
                  <p class="max-w-xs truncate text-2xs font-normal text-destructive" title={p.error}>
                    {p.error}
                  </p>
                </Show>
              </TableCell>
              <TableCell>
                <StatusBadge tone={projectTone(p)} status={p.status} />
              </TableCell>
              <TableCell class="tabular text-right">
                <span class={cn(p.servicesRunning === p.servicesTotal && p.servicesTotal > 0 && "text-success")}>
                  {p.servicesRunning}
                </span>
                <span class="text-muted-foreground">/{p.servicesTotal}</span>
              </TableCell>
              <TableCell>
                <HealthSummary id={p.id} />
              </TableCell>
              <TableCell>
                <GitCell id={p.id} />
              </TableCell>
              <TableCell class="hidden max-w-64 xl:table-cell">
                <UrlLinks urls={projectStats(p.id).urls} />
              </TableCell>
              <TableCell>
                <PrimaryAction project={p} />
              </TableCell>
            </ActionContextMenu>
          )}
        </For>
      </TableBody>
    </Table>
  );
}

function ProjectCards() {
  return (
    <ul class="flex flex-col gap-2">
      <For each={projectList()}>
        {(p) => (
          <li class="rounded-lg border bg-card p-3" {...targetAttrs({ kind: "project", project: p.id })}>
            <div class="flex items-center gap-2">
              <A href={paths.project(p.id)} class="focus-ring min-w-0 flex-1 truncate rounded-sm font-medium">
                {p.id}
              </A>
              <StatusBadge tone={projectTone(p)} status={p.status} />
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-ui text-muted-foreground">
              <span class="tabular">
                {p.servicesRunning}/{p.servicesTotal} running
              </span>
              <HealthSummary id={p.id} />
              <GitCell id={p.id} />
            </div>
            <Show when={p.error}>
              <p class="mt-1 text-2xs text-destructive">{p.error}</p>
            </Show>
            <Show when={projectStats(p.id).urls.length}>
              <UrlLinks class="mt-2" urls={projectStats(p.id).urls} />
            </Show>
            <div class="mt-2 border-t pt-2">
              <PrimaryAction project={p} />
            </div>
          </li>
        )}
      </For>
    </ul>
  );
}

function Onboarding() {
  return (
    <EmptyState
      icon={Boxes}
      title="No projects yet"
      class="rounded-xl border border-dashed py-14"
      description={
        <>
          A project is a directory with a <code class="font-mono">devyard.yml</code>. Register one from its directory:
        </>
      }
    >
      <pre class="w-full max-w-sm rounded-md bg-muted px-3 py-2 text-left font-mono text-xs">
        <span class="text-muted-foreground">$</span> cd path/to/project{"\n"}
        <span class="text-muted-foreground">$</span> devyard start
      </pre>
      <p class="text-ui text-muted-foreground">It appears here as soon as the daemon picks it up. Or add it by path:</p>
      <Button onClick={() => setAddProjectOpen(true)}>
        <Plus /> Add project
      </Button>
    </EmptyState>
  );
}

export function HomePage() {
  usePageTitle(() => "Projects");
  const totals = createMemo(() => {
    const projects = projectList();
    let running = 0;
    let total = 0;
    let failing = 0;
    for (const p of projects) {
      running += p.servicesRunning;
      total += p.servicesTotal;
      failing += projectStats(p.id).failing;
    }
    return { projects: projects.length, running, total, failing };
  });

  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Page>
        <PageHeader
          title="Projects"
          meta={
            <Show when={totals().projects}>
              <span>
                {totals().projects} {totals().projects === 1 ? "project" : "projects"}
              </span>
              <span class="tabular">
                {totals().running}/{totals().total} services running
              </span>
              <Show when={totals().failing}>
                <span class="font-medium text-destructive">{totals().failing} failing</span>
              </Show>
            </Show>
          }
          actions={
            <Show when={totals().projects}>
              <Button variant="outline" size="sm" onClick={() => setAddProjectOpen(true)}>
                <Plus /> Add project
              </Button>
            </Show>
          }
        />
        <Show when={totals().projects} fallback={<Onboarding />}>
          <Show when={!isMobile()} fallback={<ProjectCards />}>
            <ProjectsTable />
          </Show>
        </Show>
      </Page>
    </Show>
  );
}
