import { createEffect, createMemo, createSignal, Match, on, onCleanup, Show, Switch, type JSX } from "solid-js";
import { useNavigate, useParams, useSearchParams } from "@solidjs/router";
import {
  CloudDownload,
  Download,
  FileCode,
  GitBranch,
  GitCommitHorizontal,
  GitCompare,
  History,
  PanelLeft,
  PanelRight,
  RefreshCw,
  TriangleAlert,
  Upload,
  X,
} from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "~/components/ui/tabs";
import { Toggle } from "~/components/ui/toggle";
import { EmptyState } from "~/components/empty-state";
import { Meta, Page, PageHeader, PageSkeleton, usePageTitle } from "~/components/page";
import { getAction } from "~/data/actions";
import { entities, getGit, getProject, type GitEntity } from "~/data/entities";
import { errorInfo } from "~/data/errors";
import { isTypingTarget } from "~/lib/keyboard";
import { isWide } from "~/lib/media";
import { paths, type GitQuery } from "~/lib/paths";
import { createPersistedSignal } from "~/lib/persistence";
import { cn } from "~/lib/utils";
import { actionBlocked, actionPending, runAction } from "~/app/runtime";
import { ProjectNotFound } from "~/features/project/project-page";
import { AheadBehind } from "./badges";
import { CommitBox } from "./commit-box";
import { CommitList, filterCommits } from "./commit-list";
import { CommitMeta, DiffView } from "./diff-view";
import { FileList } from "./file-list";
import { DEFAULT_CONTEXT, combineLog, useGitDiff, useGitLog, WORKDIR } from "./git-data";
import { computeGitGraph, graphWidth } from "./graph";
import { listKey } from "./nav";
import { RefsPanel } from "./refs-panel";

type Tab = "commits" | "diff" | "files" | "refs";

const [panes, setPanes] = createPersistedSignal("git.panes", { refs: true, files: true }, (raw) =>
  raw && typeof raw === "object" && "refs" in raw && "files" in raw
    ? { refs: !!(raw as { refs: unknown }).refs, files: !!(raw as { files: unknown }).files }
    : undefined,
);

const SYNC = {
  fetch: { id: "git.fetch", icon: CloudDownload, running: "Fetching…" },
  pull: { id: "git.pull", icon: Download, running: "Pulling…" },
  push: { id: "git.push", icon: Upload, running: "Pushing…" },
} as const;

/**
 * Fetch/pull/push via the action registry. The spinner and the disabled state
 * follow GitStatus.sync_operation, so every client sees an operation started
 * anywhere (one runs at a time per repository).
 */
function SyncButton(props: { op: keyof typeof SYNC; git: GitEntity; project: string }) {
  const spec = () => SYNC[props.op];
  const action = () => getAction(spec().id);
  const target = () => ({ kind: "project" as const, project: props.project });
  const running = () => props.git.syncOperation === props.op || actionPending(action(), target());
  const busy = () =>
    !!props.git.syncOperation || (Object.values(SYNC) as { id: string }[]).some((s) => actionPending(getAction(s.id), target()));
  const Icon = spec().icon;
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={busy() || actionBlocked(action())}
      aria-busy={running() || undefined}
      aria-label={running() ? spec().running : action().label}
      title={running() ? spec().running : busy() ? `Waiting for ${props.git.syncOperation} to finish` : undefined}
      onClick={() => void runAction(action(), target())}
    >
      <Show when={running()} fallback={<Icon />}>
        <Spinner />
      </Show>
      <span class="hidden sm:inline">{action().label}</span>
      <Show when={props.op === "pull"}>
        <AheadBehind behind={props.git.behind} upstream={props.git.upstream} />
      </Show>
      <Show when={props.op === "push"}>
        <AheadBehind ahead={props.git.ahead} upstream={props.git.upstream} />
      </Show>
    </Button>
  );
}

function StatusSummary(props: { git: GitEntity }) {
  const g = () => props.git;
  return (
    <Show when={!g().isClean} fallback={<span>clean</span>}>
      <span class="tabular inline-flex flex-wrap gap-x-2">
        <Show when={g().staged}>
          <span class="text-success">{g().staged} staged</span>
        </Show>
        <Show when={g().dirty}>
          <span class="text-warning">{g().dirty} modified</span>
        </Show>
        <Show when={g().untracked}>
          <span>{g().untracked} untracked</span>
        </Show>
        <Show when={g().conflicts}>
          <span class="text-destructive">{g().conflicts} conflicts</span>
        </Show>
      </span>
    </Show>
  );
}

function Loading(props: { label: string }) {
  return (
    <div class="flex h-full min-h-40 w-full items-center justify-center gap-2 text-ui text-muted-foreground" role="status">
      <Spinner />
      {props.label}
    </div>
  );
}

function LoadError(props: { title: string; error: unknown; onRetry: () => void }) {
  return (
    <EmptyState
      icon={TriangleAlert}
      title={props.title}
      description={errorInfo(props.error).message || errorInfo(props.error).reason}
      class="h-full"
    >
      <Button variant="outline" size="sm" onClick={() => props.onRetry()}>
        Retry
      </Button>
    </EmptyState>
  );
}

function TabCount(props: { children: JSX.Element }) {
  return <span class="tabular rounded-full bg-muted px-1.5 font-mono text-2xs text-muted-foreground">{props.children}</span>;
}

function GitWorkspace(props: { project: string; git: GitEntity }) {
  const params = useParams<{ hash?: string }>();
  const [searchParams] = useSearchParams<{ path?: string; base?: string }>();
  const navigate = useNavigate();
  // Replaces the entry so selection/paging does not stack back-steps.
  const navigateGit = (hash: string | undefined, query: GitQuery) =>
    navigate(hash ? paths.gitCommit(props.project, hash, query) : paths.git(props.project, query), { replace: true });
  // A `?path=` on the git route switches the log to that file's history
  // (and narrows each commit's diff to it); `?base=` pins a comparison base.
  const historyPath = () => searchParams.path || undefined;
  const base = () => searchParams.base || undefined;
  const log = useGitLog(() => props.project, historyPath);
  const view = createMemo(() => combineLog(log.data?.pages));
  const commits = () => view().commits;
  const [query, setQuery] = createSignal("");
  const filtered = createMemo(() => filterCommits(commits(), query()));
  const graph = createMemo(() => computeGitGraph(commits()));
  const columns = createMemo(() => graphWidth(graph()));
  const selected = () => params.hash ?? commits()[0]?.hash;
  const workdir = () => selected() === WORKDIR;

  const [file, setFile] = createSignal<string | null>(null);
  const [fileQuery, setFileQuery] = createSignal("");
  const [context, setContext] = createSignal<number>(DEFAULT_CONTEXT);
  createEffect(on(selected, () => (setFile(null), setContext(DEFAULT_CONTEXT)), { defer: true }));

  // A comparison needs two distinct commits; a base equal to the selection
  // would be an empty diff, so it is ignored.
  const compareBase = () => {
    const b = base();
    return b && b !== selected() ? b : undefined;
  };
  const diff = useGitDiff(() => props.project, selected, context, () => historyPath() ?? "", compareBase);
  const files = () => diff.data?.files ?? [];
  const selectedFile = () => {
    const f = file();
    return f !== null && files().some((x) => x.path === f) ? f : null;
  };
  const commit = () => commits().find((c) => c.hash === selected()) ?? diff.data?.commit ?? null;
  const stagedCount = () => files().filter((f) => f.staged).length;
  const unstagedCount = () => files().filter((f) => f.unstaged || f.untracked).length;

  const [tab, setTab] = createSignal<Tab>(params.hash ? "diff" : "commits");
  let searchEl: HTMLInputElement | undefined;
  let diffScrollEl: HTMLDivElement | undefined;

  // The working tree became clean (e.g. after a commit): show HEAD instead.
  createEffect(() => {
    if (params.hash === WORKDIR && log.data && !log.isFetching && !commits().some((c) => c.hash === WORKDIR)) {
      navigateGit(undefined, { path: historyPath(), base: base() });
    }
  });

  const select = (hash: string, how: "click" | "key" | "ref") => {
    if (hash !== params.hash) navigate(paths.gitCommit(props.project, hash, { path: historyPath(), base: base() }), { replace: how === "key" });
    if (!isWide()) {
      if (how === "click") setTab("diff");
      if (how === "ref") setTab("commits");
    }
  };
  const openDiff = () => {
    if (isWide()) diffScrollEl?.focus();
    else setTab("diff");
  };
  const selectFile = (path: string | null, how: "click" | "key") => {
    setFile(path);
    if (how === "click" && !isWide()) setTab("diff");
  };
  const openHistory = (path: string) => {
    navigate(paths.gitHistory(props.project, path));
    if (!isWide()) setTab("commits");
  };
  const clearHistory = () => navigateGit(selected(), { base: base() });
  // Pin the selected (or a row's) commit as the base of a comparison.
  const setCompareBase = (hash: string) => navigateGit(selected() ?? hash, { path: historyPath(), base: hash });
  const clearCompareBase = () => navigateGit(selected(), { path: historyPath() });

  // j/k anywhere on the page steps through commits; "/" searches them.
  const onWindowKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return;
    const t = e.target instanceof Element ? e.target : null;
    if (
      isTypingTarget(t) ||
      t?.closest('[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]:not([data-git-list])')
    )
      return;
    if (e.key === "/") {
      e.preventDefault();
      if (!isWide()) setTab("commits");
      queueMicrotask(() => searchEl?.focus());
      return;
    }
    if (e.key !== "j" && e.key !== "k") return;
    const list = filtered();
    const res = listKey(
      e.key,
      list.findIndex((c) => c.hash === selected()),
      list.length,
    );
    if (res && "index" in res) {
      e.preventDefault();
      select(list[res.index]!.hash, "key");
    }
  };
  window.addEventListener("keydown", onWindowKey);
  onCleanup(() => window.removeEventListener("keydown", onWindowKey));

  const [refreshing, setRefreshing] = createSignal(false);
  const refresh = async () => {
    setRefreshing(true);
    try {
      await Promise.all([log.refetch(), diff.refetch()]);
    } finally {
      setRefreshing(false);
    }
  };

  // ------------------------------------------------------------------ panes
  const commitList = () => (
    <CommitList
      project={props.project}
      header={isWide()}
      commits={filtered()}
      total={commits().length}
      graph={graph()}
      columns={columns()}
      selected={selected()}
      query={query()}
      onQuery={setQuery}
      onSelect={select}
      onOpen={openDiff}
      searchRef={(el) => (searchEl = el)}
      hasMore={view().hasMore}
      loadingMore={log.isFetchingNextPage}
      onLoadMore={() => void log.fetchNextPage()}
      base={base()}
      onCompare={setCompareBase}
      onClearCompare={clearCompareBase}
      banner={
        <Show when={historyPath()}>
          {(p) => (
            <div class="flex items-center gap-1.5 border-b bg-primary/5 px-3 py-1.5 text-2xs">
              <History class="size-3.5 shrink-0 text-primary" />
              <span class="min-w-0 truncate font-mono" title={p()}>
                History of {p()}
              </span>
              <Button
                variant="ghost"
                size="icon-xs"
                class="ml-auto shrink-0 text-muted-foreground"
                aria-label="Clear file history"
                title="Clear file history"
                onClick={clearHistory}
              >
                <X />
              </Button>
            </div>
          )}
        </Show>
      }
      status={
        <p class="px-3 py-8 text-center text-ui text-muted-foreground">
          {commits().length
            ? `No commits match “${query()}”.`
            : historyPath()
              ? "No commits touch this file."
              : "No commits yet."}
        </p>
      }
    />
  );

  const refsPanel = (header = true) => (
    <Show when={log.data}>
      <RefsPanel project={props.project} log={view()} selected={selected()} onSelect={(h) => select(h, "ref")} header={header} />
    </Show>
  );

  const fileList = () => (
    <FileList
      header={isWide()}
      project={props.project}
      files={files()}
      workdir={workdir()}
      selected={selectedFile()}
      query={fileQuery()}
      onQuery={setFileQuery}
      onSelect={selectFile}
      onOpen={openDiff}
      onHistory={openHistory}
      onHide={isWide() ? () => setPanes({ ...panes(), files: false }) : undefined}
    />
  );

  const filesButton = () => (
    <Show when={files().length && (!isWide() || !panes().files)}>
      <Button
        variant="secondary"
        size="xs"
        onClick={() => (isWide() ? setPanes({ ...panes(), files: true }) : setTab("files"))}
        title={isWide() ? "Show the changed files pane" : "Changed files"}
      >
        <FileCode />
        Files
        <span class="tabular">{files().length}</span>
      </Button>
    </Show>
  );

  const diffPane = () => (
    <Show
      when={selected()}
      fallback={
        <EmptyState
          icon={GitCommitHorizontal}
          title="No commit selected"
          description="Pick a commit to see its changes."
          class="h-full"
        />
      }
    >
      <Switch>
        <Match when={diff.isError && !diff.data}>
          <LoadError title="Couldn't load the diff" error={diff.error} onRetry={() => void diff.refetch()} />
        </Match>
        <Match when={!diff.data}>
          <Loading label="Loading diff…" />
        </Match>
        <Match when={diff.data}>
          {(data) => (
            <DiffView
              hash={data().hash}
              diff={data().diff}
              files={data().files}
              selectedFile={selectedFile()}
              onShowAll={() => setFile(null)}
              onHistory={openHistory}
              contextLines={context()}
              onContextLines={setContext}
              loading={diff.isFetching}
              isMerge={(commit()?.parents.length ?? 0) > 1}
              project={props.project}
              workdir={workdir()}
              scrollRef={(el) => (diffScrollEl = el)}
              toolbarEnd={
                <>
                  <Show when={!workdir() && selected() && base() !== selected()}>
                    <Button
                      variant="ghost"
                      size="xs"
                      class="h-6"
                      title="Pin this commit as the comparison base, then pick another commit"
                      onClick={() => setCompareBase(selected()!)}
                    >
                      <GitCompare />
                      Compare
                    </Button>
                  </Show>
                  {filesButton()}
                </>
              }
              header={
                <div class="flex flex-col gap-2">
                  <Show when={base()}>
                    {(b) => (
                      <div class="flex items-center gap-1.5 rounded-md border border-primary/20 bg-primary/5 px-2 py-1 text-2xs text-primary">
                        <GitCompare class="size-3.5 shrink-0" />
                        <span class="min-w-0 truncate">
                          Comparing from <span class="font-mono">{b().slice(0, 7)}</span>
                          <Show when={selected() && b() !== selected()}>
                            {" to "}
                            <span class="font-mono">{selected()!.slice(0, 7)}</span>
                          </Show>
                        </span>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          class="ml-auto shrink-0 text-primary"
                          aria-label="Clear comparison"
                          title="Clear comparison"
                          onClick={clearCompareBase}
                        >
                          <X />
                        </Button>
                      </div>
                    )}
                  </Show>
                  <Show
                    when={!workdir()}
                    fallback={<CommitBox project={props.project} staged={stagedCount()} unstaged={unstagedCount()} />}
                  >
                    <Show when={commit()}>{(c) => <CommitMeta commit={c()} onSelectCommit={(h) => select(h, "ref")} />}</Show>
                  </Show>
                </div>
              }
            />
          )}
        </Match>
      </Switch>
    </Show>
  );

  return (
    <Page wide class="h-full min-h-0 gap-3 px-3 py-3 md:gap-4 md:px-4 md:py-4">
      <PageHeader
        title="Git"
        badges={
          <>
            <span class="inline-flex h-6 items-center gap-1 rounded-md border border-success/30 bg-success/10 px-2 font-mono text-xs font-medium text-success">
              <GitBranch class="size-3.5" />
              {props.git.branch || `detached at ${props.git.headHash.slice(0, 7)}`}
            </span>
            <AheadBehind ahead={props.git.ahead} behind={props.git.behind} upstream={props.git.upstream} />
          </>
        }
        meta={
          <>
            <Show when={props.git.upstream}>
              <Meta label="upstream">
                <span class="font-mono text-2xs">{props.git.upstream}</span>
              </Meta>
            </Show>
            <Show when={props.git.headHash}>
              <Meta label="HEAD">
                <span class="font-mono text-2xs">{props.git.headHash.slice(0, 7)}</span>
              </Meta>
            </Show>
            <Meta label="status">
              <StatusSummary git={props.git} />
            </Meta>
          </>
        }
        actions={
          <>
            <SyncButton op="fetch" git={props.git} project={props.project} />
            <SyncButton op="pull" git={props.git} project={props.project} />
            <SyncButton op="push" git={props.git} project={props.project} />
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Refresh"
              title="Refresh"
              disabled={refreshing()}
              onClick={() => void refresh()}
            >
              <RefreshCw class={cn(refreshing() && "animate-spin")} />
            </Button>
            <Show when={isWide()}>
              <span class="mx-1 h-5 w-px bg-border" aria-hidden="true" />
              <PaneToggle pressed={panes().refs} onChange={(v) => setPanes({ ...panes(), refs: v })} label="Show refs pane">
                <PanelLeft />
              </PaneToggle>
              <PaneToggle
                pressed={panes().files}
                onChange={(v) => setPanes({ ...panes(), files: v })}
                label="Show changed files pane"
              >
                <PanelRight />
              </PaneToggle>
            </Show>
          </>
        }
      />
      <div class="flex min-h-0 flex-1 overflow-hidden rounded-lg border bg-card">
        <Switch>
          <Match when={log.isError && !log.data}>
            <LoadError title="Couldn't load the git log" error={log.error} onRetry={() => void log.refetch()} />
          </Match>
          <Match when={!log.data}>
            <Loading label="Loading commits…" />
          </Match>
          <Match when={isWide()}>
            <Show when={panes().refs}>
              <aside class="w-48 shrink-0 border-r 2xl:w-56" aria-label="Refs">
                {refsPanel()}
              </aside>
            </Show>
            <section class="w-72 shrink-0 border-r xl:w-80 2xl:w-96" aria-label="Commits">
              {commitList()}
            </section>
            <section class="min-w-0 flex-1" aria-label="Changes">
              {diffPane()}
            </section>
            <Show when={panes().files && diff.data && files().length}>
              <aside class="w-56 shrink-0 border-l 2xl:w-64" aria-label="Changed files">
                {fileList()}
              </aside>
            </Show>
          </Match>
          <Match when={true}>
            <Tabs value={tab()} onChange={(v: string) => setTab(v as Tab)} class="flex min-h-0 w-full flex-1 flex-col gap-0">
              <TabsList variant="line" class="no-scrollbar h-10 w-full shrink-0 justify-start overflow-x-auto border-b px-2">
                <TabsTrigger value="commits" class="flex-none gap-1.5">
                  <GitCommitHorizontal class="max-sm:hidden" />
                  Commits
                  <TabCount>{filtered().length}</TabCount>
                </TabsTrigger>
                <TabsTrigger value="diff" class="flex-none gap-1.5">
                  <FileCode class="max-sm:hidden" />
                  Diff
                  <Show when={selected()}>{(h) => <TabCount>{h() === WORKDIR ? "work" : h().slice(0, 7)}</TabCount>}</Show>
                </TabsTrigger>
                <TabsTrigger value="files" class="flex-none gap-1.5">
                  <FileCode class="max-sm:hidden" />
                  Files
                  <Show when={files().length}>
                    <TabCount>{files().length}</TabCount>
                  </Show>
                </TabsTrigger>
                <TabsTrigger value="refs" class="flex-none gap-1.5">
                  <GitBranch class="max-sm:hidden" />
                  Refs
                  <TabCount>{view().branches.filter((b) => !b.isRemote).length}</TabCount>
                </TabsTrigger>
              </TabsList>
              <TabsContent value="commits" class="min-h-0 flex-1">
                {commitList()}
              </TabsContent>
              <TabsContent value="diff" class="min-h-0 flex-1">
                {diffPane()}
              </TabsContent>
              <TabsContent value="files" class="min-h-0 flex-1">
                <Show when={diff.data} fallback={<Loading label="Loading files…" />}>
                  {fileList()}
                </Show>
              </TabsContent>
              <TabsContent value="refs" class="min-h-0 flex-1">
                {refsPanel(false)}
              </TabsContent>
            </Tabs>
          </Match>
        </Switch>
      </div>
    </Page>
  );
}

function PaneToggle(props: { pressed: boolean; onChange: (v: boolean) => void; label: string; children: JSX.Element }) {
  return (
    <Toggle size="sm" pressed={props.pressed} onPressedChange={props.onChange} aria-label={props.label} title={props.label}>
      {props.children}
    </Toggle>
  );
}

export function GitPage() {
  const params = useParams<{ project: string; hash?: string }>();
  const git = () => getGit(params.project);
  usePageTitle(() => `Git · ${params.project}`);

  return (
    <Show when={entities.state.loaded} fallback={<PageSkeleton />}>
      <Show when={getProject(params.project)} fallback={<ProjectNotFound id={params.project} />}>
        <Show
          when={git()?.isRepo ? git() : undefined}
          fallback={
            <EmptyState
              icon={GitBranch}
              title="Not a git repository"
              description={`${params.project}'s directory isn't inside a git repository.`}
              class="h-full"
            />
          }
        >
          {(g) => (
            <Show when={params.project} keyed>
              {(project) => <GitWorkspace project={project} git={g()} />}
            </Show>
          )}
        </Show>
      </Show>
    </Show>
  );
}
