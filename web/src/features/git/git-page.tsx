import { Show } from "solid-js";
import { useParams } from "@solidjs/router";
import { GitBranch } from "lucide-solid";
import { EmptyState } from "~/components/empty-state";
import { Page, PageHeader, PageSkeleton, usePageTitle } from "~/components/page";
import { entities, getGit, getProject } from "~/data/entities";
import { ProjectNotFound } from "~/features/project/project-page";

/**
 * Placeholder route: the full git view (log graph, diff, stage/commit,
 * push/pull/fetch) is ported separately onto GitLog/GitDiff/… queries keyed
 * by queryKeys.git(project), which Watch invalidates on change_seq.
 */
export function GitPage() {
  const params = useParams<{ project: string }>();
  const git = () => getGit(params.project);
  usePageTitle(() => `Git · ${params.project}`);
  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Show when={getProject(params.project)} fallback={<ProjectNotFound id={params.project} />}>
        <Page>
          <PageHeader
            title="Git"
            meta={
              <Show when={git()?.isRepo} fallback={<span>Not a git repository.</span>}>
                <span>
                  {git()!.branch || git()!.headHash.slice(0, 7)}
                  {git()!.upstream ? ` → ${git()!.upstream}` : ""}
                </span>
                <span class="tabular">
                  ↑{git()!.ahead} ↓{git()!.behind}
                </span>
                <span class="tabular">
                  {git()!.staged} staged · {git()!.dirty} modified · {git()!.untracked} untracked
                </span>
              </Show>
            }
          />
          <EmptyState icon={GitBranch} title="Git view coming soon" description="Commit history, diffs and staging will appear here." />
        </Page>
      </Show>
    </Show>
  );
}
