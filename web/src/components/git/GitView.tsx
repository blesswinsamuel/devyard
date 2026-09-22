import { For, Show, createEffect, createMemo, createSignal } from "solid-js";
import {
  Archive,
  ArrowLeft,
  Check,
  ChevronDown,
  ChevronRight,
  CloudDownload,
  Copy,
  Download,
  FileCode,
  FileText,
  GitBranch,
  GitCommitHorizontal,
  Menu,
  Minus,
  MoreHorizontal,
  PanelLeft,
  PanelLeftClose,
  PanelRight,
  PanelRightClose,
  Plus,
  RefreshCw,
  Search,
  Send,
  Tag,
  Upload,
} from "lucide-solid";
import {
  commitGitChanges,
  fetchGit,
  gitBranches,
  gitCommitError,
  gitCommitLoading,
  gitCommits,
  gitDiffLoading,
  gitDiffs,
  gitError,
  gitLoading,
  gitStashes,
  gitSync,
  gitTags,
  loadGitDiff,
  loadGitLog,
  pullGit,
  pushGit,
  selectCommit,
  selectDiffFile,
  selectedCommitHash,
  selectedFilePath,
  stageGitFile,
} from "~/stores/data";
import { setSidebarOpen } from "~/stores/app";
import { closeGitView, selectedProject } from "~/stores/nav";
import type { GitBranch as GitBranchType, GitFileChange } from "~/lib/types";
import { computeGitGraph } from "~/lib/git_graph";
import { CommitListRow } from "~/components/git/CommitListRow";
import { parseDiff, parseGitMeta } from "~/lib/diff";
import { formatAuthorTime, formatRelativeTime } from "~/lib/format";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Card } from "~/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import { Separator } from "~/components/ui/separator";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { cn } from "~/lib/utils";

function statusBadge(status: string): { label: string; class: string } {
  const st = (status || "M").toUpperCase()[0]!;
  switch (st) {
    case "A":
      return { label: "A", class: "bg-success/15 text-success border-success/25" };
    case "D":
      return { label: "D", class: "bg-destructive/15 text-destructive border-destructive/25" };
    case "R":
      return { label: "R", class: "bg-info/15 text-info border-info/25" };
    default:
      return { label: "M", class: "bg-warning/15 text-warning border-warning/25" };
  }
}

function AheadBehindPill(props: { kind: "ahead" | "behind"; count?: number; upstream?: string; class?: string }) {
  return (
    <Show when={(props.count ?? 0) > 0}>
      <span
        class={cn(
          "shrink-0 rounded-full px-1.5 py-px font-mono text-[10px] tabular",
          props.kind === "ahead" ? "bg-success/12 text-success" : "bg-warning/12 text-warning",
          props.class,
        )}
        title={
          props.kind === "ahead"
            ? `${props.count} ahead of ${props.upstream || "upstream"}`
            : `${props.count} behind ${props.upstream || "upstream"}`
        }
      >
        {props.kind === "ahead" ? "↑" : "↓"}
        {props.count}
      </span>
    </Show>
  );
}

export function GitView() {
  const project = () => selectedProject();
  const commits = createMemo(() => (project() ? gitCommits()[project()!] ?? [] : []));
  const branches = createMemo(() => (project() ? gitBranches()[project()!] ?? [] : []));
  const tags = createMemo(() => (project() ? gitTags()[project()!] ?? [] : []));
  const stashes = createMemo(() => (project() ? gitStashes()[project()!] ?? [] : []));

  const activeBranch = createMemo(() => branches().find((b) => b.isActive));
  const upstreamName = createMemo(() => activeBranch()?.upstream ?? "");
  const aheadCount = createMemo(() => activeBranch()?.ahead ?? 0);
  const behindCount = createMemo(() => activeBranch()?.behind ?? 0);
  const localBranches = createMemo(() => branches().filter((b) => !b.isRemote));
  const remoteBranches = createMemo(() => branches().filter((b) => b.isRemote));
  const remoteNames = createMemo(() => {
    const names = new Set<string>();
    for (const b of remoteBranches()) {
      const idx = b.name.indexOf("/");
      if (idx > 0) names.add(b.name.slice(0, idx));
    }
    return [...names].sort();
  });

  const [branchesOpen, setBranchesOpen] = createSignal(true);
  const [remotesOpen, setRemotesOpen] = createSignal(true);
  const [tagsOpen, setTagsOpen] = createSignal(true);
  const [stashesOpen, setStashesOpen] = createSignal(true);
  const [openRemotes, setOpenRemotes] = createSignal<Record<string, boolean>>({});

  // Pane visibility for desktop / laptop view
  const storedBranchesPane = localStorage.getItem("lc-git-branches-pane");
  const [showBranchesPane, setShowBranchesPane] = createSignal(storedBranchesPane === null ? true : storedBranchesPane === "true");

  const storedFilesPane = localStorage.getItem("lc-git-files-pane");
  const [showFilesPane, setShowFilesPane] = createSignal(storedFilesPane === null ? true : storedFilesPane === "true");

  createEffect(() => {
    localStorage.setItem("lc-git-branches-pane", String(showBranchesPane()));
  });

  createEffect(() => {
    localStorage.setItem("lc-git-files-pane", String(showFilesPane()));
  });

  const error = createMemo(() => (project() ? gitError()[project()!] ?? "" : ""));
  const loading = createMemo(() => (project() ? !!gitLoading()[project()!] : false));

  const currentCommitHash = createMemo(() => (project() ? selectedCommitHash()[project()!] ?? null : null));
  const currentFilePath = createMemo(() => (project() ? selectedFilePath()[project()!] ?? null : null));

  const [searchQuery, setSearchQuery] = createSignal("");
  const [fileSearchQuery, setFileSearchQuery] = createSignal("");
  const [commitMessage, setCommitMessage] = createSignal("");
  const [mobileTab, setMobileTab] = createSignal<"commits" | "diff" | "files" | "branches">("commits");

  const isCommitting = createMemo(() => (project() ? !!gitCommitLoading()[project()!] : false));
  const commitErr = createMemo(() => (project() ? gitCommitError()[project()!] ?? "" : ""));

  // Remote git operation currently running for this project ("" = idle), broadcast to all clients.
  const syncOp = createMemo(() => (project() ? gitSync()[project()!] ?? "" : ""));
  const isSyncing = createMemo(() => syncOp() !== "");

  const graphMap = createMemo(() => computeGitGraph(commits()));
  const maxColumns = createMemo(() => {
    let max = 1;
    for (const info of graphMap().values()) {
      if (info.activeWidth > max) max = info.activeWidth;
    }
    return max;
  });

  const filteredCommits = createMemo(() => {
    const q = searchQuery().toLowerCase().trim();
    if (!q) return commits();
    return commits().filter(
      (c) =>
        c.subject.toLowerCase().includes(q) ||
        c.author.toLowerCase().includes(q) ||
        c.hash.toLowerCase().includes(q)
    );
  });

  // Auto-select the first commit once the log arrives.
  createEffect(() => {
    const list = commits();
    const proj = project();
    if (proj && list.length > 0 && !selectedCommitHash()[proj]) {
      selectCommit(proj, list[0]!.hash, { skipPush: true });
    }
  });

  const diffResult = createMemo(() => {
    const proj = project();
    const hash = currentCommitHash();
    if (!proj || !hash) return null;
    return gitDiffs()[proj]?.[hash] ?? null;
  });
  const diffLoading = createMemo(() => (project() ? !!gitDiffLoading()[project()!] : false));
  const isWorkdir = createMemo(() => currentCommitHash() === "WORKDIR");

  const allFiles = createMemo(() => diffResult()?.files ?? []);
  const fileFilter = createMemo(() => fileSearchQuery().toLowerCase().trim());
  const matchFilter = (f: GitFileChange) => !fileFilter() || f.path.toLowerCase().includes(fileFilter());
  const stagedFiles = createMemo(() => allFiles().filter((f) => f.staged && matchFilter(f)));
  const unstagedFiles = createMemo(() => allFiles().filter((f) => f.unstaged && matchFilter(f)));
  const untrackedFiles = createMemo(() => allFiles().filter((f) => f.untracked && matchFilter(f)));
  const totalAdditions = createMemo(() => {
    const diff = diffResult();
    if (!diff) return 0;
    if (diff.commit?.additions) return diff.commit.additions;
    return (diff.files ?? []).reduce((acc, f) => acc + (f.additions || 0), 0);
  });
  const totalDeletions = createMemo(() => {
    const diff = diffResult();
    if (!diff) return 0;
    if (diff.commit?.deletions) return diff.commit.deletions;
    return (diff.files ?? []).reduce((acc, f) => acc + (f.deletions || 0), 0);
  });

  const handleCommitSubmit = (e: Event) => {
    e.preventDefault();
    const proj = project();
    const msg = commitMessage().trim();
    if (!proj || !msg || isCommitting()) return;
    commitGitChanges(proj, msg);
    setCommitMessage("");
  };

  // Commit-list keyboard navigation.
  let commitListRef: HTMLDivElement | undefined;
  const selectCommitByIndex = (next: number) => {
    const proj = project();
    const list = filteredCommits();
    if (!proj || list.length === 0) return;
    const clamped = Math.max(0, Math.min(list.length - 1, next));
    const hash = list[clamped]?.hash;
    if (!hash) return;
    selectCommit(proj, hash);
    requestAnimationFrame(() => {
      commitListRef
        ?.querySelector(`[data-commit="${CSS.escape(hash)}"]`)
        ?.scrollIntoView({ block: "nearest" });
    });
  };

  const onCommitClicked = (hash: string) => {
    if (!project()) return;
    selectCommit(project()!, hash);
    setMobileTab("diff");
  };

  const onFileClicked = (filePath: string | null) => {
    if (!project()) return;
    selectDiffFile(project()!, filePath);
    setMobileTab("diff");
  };

  const onRefClicked = (hash: string) => {
    if (!project()) return;
    selectCommit(project()!, hash);
    setMobileTab("commits");
  };

  // --- Sub-views for reuse in desktop and mobile tabs ---

  const renderRefsSidebar = () => (
    <div class="flex h-full select-none flex-col divide-y overflow-y-auto bg-card/40">
      <div class="hidden lg:flex h-8 shrink-0 items-center justify-between border-b bg-muted/20 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        <span class="flex items-center gap-1.5">
          <GitBranch class="size-3.5" />
          Branches
        </span>
        <Button
          variant="ghost"
          size="icon-xs"
          class="size-5 rounded p-0 text-muted-foreground hover:text-foreground"
          onClick={() => setShowBranchesPane(false)}
          title="Hide branches pane"
        >
          <PanelLeftClose class="size-3.5" />
        </Button>
      </div>

      <RefSection
        icon={<GitBranch class="size-3 text-success" />}
        label={`Branches (${branches().length})`}
        open={branchesOpen()}
        onToggle={() => setBranchesOpen(!branchesOpen())}
      >
        <For each={localBranches()}>
          {(b) => (
            <RefRow
              name={b.name}
              hash={b.hash}
              active={!!b.isActive}
              badge={b.isActive ? "HEAD" : undefined}
              ahead={b.ahead}
              behind={b.behind}
              upstream={b.upstream}
              onSelect={() => onRefClicked(b.hash)}
            />
          )}
        </For>
      </RefSection>

      <RefSection
        icon={<GitBranch class="size-3 text-muted-foreground" />}
        label={`Remotes (${remoteBranches().length})`}
        open={remotesOpen()}
        onToggle={() => setRemotesOpen(!remotesOpen())}
      >
        <Show when={remoteNames().length > 0} fallback={<p class="px-3 py-2 text-[11px] italic text-muted-foreground">No remotes</p>}>
          <For each={remoteNames()}>
            {(remoteName) => {
              const expanded = () => !!openRemotes()[remoteName];
              const list = () =>
                remoteBranches()
                  .filter((b) => b.name.startsWith(remoteName + "/"))
                  .map((b) => ({ ...b, shortName: b.name.slice(remoteName.length + 1) }));
              return (
                <>
                  <button
                    type="button"
                    onClick={() => setOpenRemotes((prev) => ({ ...prev, [remoteName]: !prev[remoteName] }))}
                    class="flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-[11px] text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
                  >
                    <span class="truncate font-mono">{remoteName}</span>
                    <span class="flex items-center gap-1">
                      <span class="font-mono tabular opacity-70">{list().length}</span>
                      {expanded() ? <ChevronDown class="size-3" /> : <ChevronRight class="size-3" />}
                    </span>
                  </button>
                  <Show when={expanded()}>
                    <For each={list()}>
                      {(b) => (
                        <button
                          type="button"
                          onClick={() => onRefClicked(b.hash)}
                          title={`${b.name} (${b.hash.substring(0, 7)})`}
                          class="w-full truncate py-1.5 pl-7 pr-3 text-left font-mono text-[11px] text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
                        >
                          {b.shortName}
                        </button>
                      )}
                    </For>
                  </Show>
                </>
              );
            }}
          </For>
        </Show>
      </RefSection>

      <RefSection
        icon={<Tag class="size-3 text-warning" />}
        label={`Tags (${tags().length})`}
        open={tagsOpen()}
        onToggle={() => setTagsOpen(!tagsOpen())}
      >
        <Show when={tags().length > 0} fallback={<p class="px-3 py-2 text-[11px] italic text-muted-foreground">No tags</p>}>
          <For each={tags()}>
            {(t) => (
              <RefRow name={t.name} hash={t.hash} onSelect={() => onRefClicked(t.hash)} />
            )}
          </For>
        </Show>
      </RefSection>

      <RefSection
        icon={<Archive class="size-3 text-muted-foreground" />}
        label={`Stashes (${stashes().length})`}
        open={stashesOpen()}
        onToggle={() => setStashesOpen(!stashesOpen())}
      >
        <Show when={stashes().length > 0} fallback={<p class="px-3 py-2 text-[11px] italic text-muted-foreground">No stashes</p>}>
          <For each={stashes()}>
            {(s) => (
              <button
                type="button"
                onClick={() => onRefClicked(s.hash)}
                title={`${s.index}: ${s.name}`}
                class="flex w-full min-w-0 flex-col gap-0.5 px-3 py-1.5 text-left text-xs transition-colors hover:bg-muted/60"
              >
                <span class="truncate font-mono text-[11px]">{s.index}</span>
                <span class="truncate pl-0.5 text-[10px] leading-tight text-muted-foreground">{s.name}</span>
              </button>
            )}
          </For>
        </Show>
      </RefSection>
    </div>
  );

  const renderCommitList = () => (
    <div class="flex h-full flex-col bg-background">
      <div class="flex h-8 shrink-0 items-center justify-between border-b bg-muted/20 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        <span class="flex items-center gap-1.5">
          <Show when={!showBranchesPane()}>
            <Button
              variant="ghost"
              size="icon-xs"
              class="hidden lg:flex mr-0.5 size-5 rounded p-0 text-muted-foreground hover:text-foreground"
              onClick={() => setShowBranchesPane(true)}
              title="Show branches pane"
            >
              <PanelLeft class="size-3.5" />
            </Button>
          </Show>
          <GitCommitHorizontal class="size-3.5" />
          Commits
        </span>
        <span class="tabular">{filteredCommits().length}</span>
      </div>

      {/* Mobile search bar if on mobile tab */}
      <div class="border-b p-1.5 sm:hidden">
        <div class="relative">
          <Search class="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Search commits…"
            value={searchQuery()}
            onInput={(e) => setSearchQuery(e.currentTarget.value)}
            class="h-7 w-full rounded-md bg-background pl-7 pr-2 text-xs"
          />
        </div>
      </div>

      <div
        ref={commitListRef}
        tabIndex={0}
        data-kbd-ignore
        class="min-h-0 flex-1 overflow-y-auto outline-none"
        onKeyDown={(e) => {
          if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
          const list = filteredCommits();
          if (list.length === 0) return;
          e.preventDefault();
          const cur = currentCommitHash();
          const idx = list.findIndex((c) => c.hash === cur);
          selectCommitByIndex(e.key === "ArrowDown" ? (idx < 0 ? 0 : idx + 1) : idx <= 0 ? 0 : idx - 1);
        }}
      >
        <Show
          when={!loading() || commits().length > 0}
          fallback={
            <div class="flex h-full items-center justify-center gap-2 p-6 text-muted-foreground">
              <Spinner />
              <span class="text-xs">Loading commits…</span>
            </div>
          }
        >
          <Show
            when={!error()}
            fallback={
              <div class="p-4 text-center">
                <Alert variant="destructive" class="mb-2">
                  <AlertDescription class="break-words text-xs">{error()}</AlertDescription>
                </Alert>
                <Button variant="outline" size="xs" onClick={() => project() && loadGitLog(project()!)}>
                  Retry
                </Button>
              </div>
            }
          >
            <Show
              when={filteredCommits().length > 0}
              fallback={
                <Empty class="border-0 p-6">
                  <EmptyHeader>
                    <EmptyTitle class="text-xs font-normal">
                      {commits().length === 0 ? "No commits in repository." : "No matching commits."}
                    </EmptyTitle>
                  </EmptyHeader>
                </Empty>
              }
            >
              <For each={filteredCommits()}>
                {(commit) => (
                  <CommitListRow
                    commit={commit}
                    info={graphMap().get(commit.hash)}
                    columns={maxColumns()}
                    selected={currentCommitHash() === commit.hash}
                    onSelect={() => onCommitClicked(commit.hash)}
                  />
                )}
              </For>
            </Show>
          </Show>
        </Show>
      </div>
    </div>
  );

  const renderFilesList = () => (
    <div class="flex h-full flex-col bg-card/40">
      <div class="flex h-8 shrink-0 items-center justify-between border-b bg-muted/20 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        <span class="flex items-center gap-1.5">
          <FileCode class="size-3.5" />
          Changed Files
          <span class="tabular">{allFiles().length}</span>
        </span>
        <Button
          variant="ghost"
          size="icon-xs"
          class="hidden lg:flex size-5 rounded p-0 text-muted-foreground hover:text-foreground"
          onClick={() => setShowFilesPane(false)}
          title="Hide changed files pane"
        >
          <PanelRightClose class="size-3.5" />
        </Button>
      </div>
      <div class="border-b p-1.5">
        <div class="relative">
          <Search class="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Filter files…"
            value={fileSearchQuery()}
            onInput={(e) => setFileSearchQuery(e.currentTarget.value)}
            class="h-7 w-full rounded-md bg-background pl-7 pr-2 text-xs"
          />
        </div>
      </div>
      <div data-kbd-ignore tabIndex={0} class="min-h-0 flex-1 overflow-y-auto outline-none">
        <FileRowAll
          count={allFiles().length}
          additions={totalAdditions()}
          deletions={totalDeletions()}
          selected={currentFilePath() === null}
          onSelect={() => onFileClicked(null)}
        />
        <Show
          when={isWorkdir()}
          fallback={
            <For each={allFiles().filter((f) => f.path.toLowerCase().includes(fileFilter()))}>
              {(file) => (
                <FileRow
                  file={file}
                  selected={currentFilePath() === file.path}
                  onSelect={() => onFileClicked(file.path)}
                />
              )}
            </For>
          }
        >
          <FileGroup
            label={`Staged (${stagedFiles().length})`}
            tone="staged"
            actionLabel="Unstage all"
            onAction={() => project() && stageGitFile(project()!, "", true, true)}
            files={stagedFiles()}
            selectedPath={currentFilePath()}
            onSelect={(f) => onFileClicked(f.path)}
            onFileAction={(f) => project() && stageGitFile(project()!, f.path, true)}
            fileActionLabel="Unstage"
          />
          <FileGroup
            label={`Unstaged (${unstagedFiles().length + untrackedFiles().length})`}
            tone="unstaged"
            actionLabel="Stage all"
            onAction={() => project() && stageGitFile(project()!, "", false, true)}
            files={[...unstagedFiles(), ...untrackedFiles()]}
            selectedPath={currentFilePath()}
            onSelect={(f) => onFileClicked(f.path)}
            onFileAction={(f) => project() && stageGitFile(project()!, f.path, false)}
            fileActionLabel="Stage"
          />
        </Show>
      </div>
    </div>
  );

  const renderDiffArea = () => (
    <div class="flex h-full min-w-0 flex-1 flex-col overflow-hidden bg-background">
      <Show
        when={currentCommitHash()}
        fallback={
          <Empty class="h-full border-0">
            <EmptyHeader>
              <EmptyMedia>
                <GitCommitHorizontal class="size-8 stroke-1 text-muted-foreground" />
              </EmptyMedia>
              <EmptyTitle>No commit selected</EmptyTitle>
              <EmptyDescription>Select a commit to view its diff.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        }
      >
        <Show
          when={!diffLoading() || !!diffResult()}
          fallback={
            <div class="flex h-full items-center justify-center gap-2 text-muted-foreground">
              <Spinner />
              <span class="text-xs">Loading diff…</span>
            </div>
          }
        >
          {/* Commit meta / worktree form */}
          <div class="flex shrink-0 flex-col gap-2 border-b bg-muted/20 p-3">
            <Show
              when={isWorkdir()}
              fallback={
                <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                  <div class="min-w-0 flex-1">
                    <h2 class="text-sm font-semibold leading-snug">{diffResult()?.commit?.subject}</h2>
                    <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
                      <span class="font-medium text-foreground">{diffResult()?.commit?.author}</span>
                      <span class="truncate">&lt;{diffResult()?.commit?.email}&gt;</span>
                      <span>·</span>
                      <span>{diffResult()?.commit?.time ? formatAuthorTime(diffResult()!.commit!.time) : ""}</span>
                      <Show when={totalAdditions() > 0 || totalDeletions() > 0}>
                        <span>·</span>
                        <span class="flex items-center gap-1 font-mono text-[11px] tabular">
                          <Show when={totalAdditions() > 0}>
                            <span class="text-success font-medium">+{totalAdditions()}</span>
                          </Show>
                          <Show when={totalDeletions() > 0}>
                            <span class="text-destructive font-medium">−{totalDeletions()}</span>
                          </Show>
                        </span>
                      </Show>
                    </div>
                  </div>
                  <div class="flex shrink-0 items-center gap-2">
                    <CopyHash hash={diffResult()?.commit?.hash ?? ""} />
                    <Show when={!showFilesPane() && allFiles().length > 0}>
                      <Button
                        variant="secondary"
                        size="xs"
                        class="hidden lg:flex h-6 gap-1 px-2 text-[11px]"
                        onClick={() => setShowFilesPane(true)}
                        title="Show changed files pane"
                      >
                        <FileCode class="size-3" />
                        Files ({allFiles().length})
                      </Button>
                    </Show>
                  </div>
                </div>
              }
            >
              <form onSubmit={handleCommitSubmit} class="flex flex-col gap-2">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <div class="flex items-center gap-2">
                    <Badge class="border-warning/40 bg-warning/15 text-warning">Uncommitted changes</Badge>
                    <span class="text-[11px] tabular text-muted-foreground">
                      {stagedFiles().length} staged · {unstagedFiles().length + untrackedFiles().length} unstaged
                    </span>
                  </div>
                  <div class="flex items-center gap-1.5">
                    <Show when={allFiles().length > 0}>
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        class="lg:hidden text-xs text-muted-foreground"
                        onClick={() => setMobileTab("files")}
                      >
                        <FileCode class="!size-3.5" />
                        Manage files ({allFiles().length})
                      </Button>
                    </Show>
                    <Show when={!showFilesPane() && allFiles().length > 0}>
                      <Button
                        type="button"
                        variant="secondary"
                        size="xs"
                        class="hidden lg:flex h-6 gap-1 px-2 text-[11px]"
                        onClick={() => setShowFilesPane(true)}
                        title="Show changed files pane"
                      >
                        <FileCode class="size-3" />
                        Files ({allFiles().length})
                      </Button>
                    </Show>
                  </div>
                </div>
                <div class="flex flex-col gap-2 sm:flex-row">
                  <Textarea
                    placeholder="Commit message… (⌘⏎ to commit)"
                    value={commitMessage()}
                    onInput={(e) => setCommitMessage(e.currentTarget.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) handleCommitSubmit(e);
                    }}
                    rows={2}
                    class="min-h-0 flex-1 resize-none rounded-md bg-background p-2 text-xs"
                  />
                  <Button
                    type="submit"
                    size="sm"
                    class="h-auto self-end sm:self-stretch px-4"
                    disabled={!commitMessage().trim() || isCommitting()}
                  >
                    <Show when={isCommitting()} fallback={<Send class="!size-3.5" />}>
                      <Spinner class="size-3.5" />
                    </Show>
                    Commit
                  </Button>
                </div>
                <Show when={commitErr()}>
                  <Alert variant="destructive" class="py-1 px-2">
                    <AlertDescription class="text-xs">{commitErr()}</AlertDescription>
                  </Alert>
                </Show>
              </form>
            </Show>

            {/* Mobile/Compact active file indicator with switcher */}
            <Show when={allFiles().length > 0 && (currentFilePath() || !showFilesPane())}>
              <div class="flex items-center justify-between gap-2 rounded-md bg-muted/40 px-2 py-1 text-xs">
                <span class="truncate font-mono text-[11px] text-muted-foreground">
                  <Show when={currentFilePath()} fallback={<span class="font-sans">Showing all files ({allFiles().length})</span>}>
                    {(path) => (
                      <span class="flex items-center gap-1">
                        <FileCode class="size-3 shrink-0 text-primary" />
                        <span class="truncate">{path()}</span>
                      </span>
                    )}
                  </Show>
                </span>
                <div class="flex shrink-0 items-center gap-1">
                  <Show when={currentFilePath()}>
                    <Button
                      variant="ghost"
                      size="xs"
                      class="h-5 px-1.5 text-[10px]"
                      onClick={() => selectDiffFile(project()!, null)}
                    >
                      Show all
                    </Button>
                  </Show>
                  <Button
                    variant="secondary"
                    size="xs"
                    class="h-5 px-1.5 text-[10px] lg:hidden"
                    onClick={() => setMobileTab("files")}
                  >
                    Files ({allFiles().length})
                  </Button>
                </div>
              </div>
            </Show>
          </div>

          {/* Diff body */}
          <div tabIndex={0} data-kbd-ignore class="min-h-0 flex-1 overflow-y-auto p-2 sm:p-3 font-mono text-[11.5px] leading-relaxed outline-none">
            <Show
              when={diffResult()?.diff}
              fallback={
                <Empty class="border-0 py-10">
                  <EmptyHeader>
                    <EmptyTitle class="text-xs font-normal">No changes.</EmptyTitle>
                  </EmptyHeader>
                </Empty>
              }
            >
              <DiffBody
                project={project()!}
                hash={currentCommitHash()!}
                diff={diffResult()!.diff}
                files={allFiles()}
                selectedFile={currentFilePath()}
                loading={diffLoading()}
              />
            </Show>
          </div>
        </Show>
      </Show>
    </div>
  );

  return (
    <div class="flex h-full min-w-0 flex-col overflow-hidden bg-background">
      {/* Top bar */}
      <div class="flex h-11 shrink-0 items-center justify-between gap-1.5 border-b bg-card/50 px-2 sm:px-3 backdrop-blur">
        <div class="flex min-w-0 items-center gap-1 sm:gap-1.5">
          <Button
            variant="ghost"
            size="icon-sm"
            class="shrink-0 text-muted-foreground md:hidden"
            onClick={() => setSidebarOpen(true)}
            aria-label="Open menu"
          >
            <Menu class="!size-4" />
          </Button>

          <Button
            variant="ghost"
            size="xs"
            class="shrink-0 text-muted-foreground"
            onClick={() => closeGitView()}
            aria-label="Back to logs"
          >
            <ArrowLeft class="!size-3.5" />
            <span class="hidden xs:inline sm:inline">Back</span>
          </Button>

          <Separator orientation="vertical" class="mx-0.5 sm:mx-1 h-4" />
          <GitBranch class="size-3.5 shrink-0 text-primary" />
          <span class="truncate text-[13px] font-semibold">{project()}</span>

          <Show when={activeBranch()}>
            {(b) => (
              <div class="flex shrink-0 items-center gap-1">
                <Badge class="border-success/35 bg-success/12 font-mono text-[10px] text-success">
                  {b().name}
                </Badge>
                <AheadBehindPill kind="ahead" count={b().ahead} upstream={b().upstream} />
                <AheadBehindPill kind="behind" count={b().behind} upstream={b().upstream} />
              </div>
            )}
          </Show>
        </div>

        <div class="flex shrink-0 items-center gap-1">
          {/* Desktop Search input */}
          <div class="hidden sm:block">
            <Input
              type="text"
              placeholder="Search commits…"
              value={searchQuery()}
              onInput={(e) => setSearchQuery(e.currentTarget.value)}
              class="h-7 w-36 rounded-md bg-background px-2 text-xs sm:w-48 lg:w-56"
            />
          </div>

          {/* Desktop Actions */}
          <div class="hidden md:flex items-center gap-1">
            <Button
              variant="ghost"
              size="xs"
              class="text-muted-foreground"
              disabled={isSyncing()}
              title={syncOp() === "fetch" ? "Fetching…" : undefined}
              onClick={() => project() && fetchGit(project()!)}
            >
              <Show when={syncOp() === "fetch"} fallback={<CloudDownload class="!size-3.5" />}>
                <Spinner class="size-3.5" />
              </Show>
              Fetch
            </Button>
            <Button
              variant="ghost"
              size="xs"
              class="text-muted-foreground"
              disabled={isSyncing()}
              title={syncOp() === "pull" ? "Pulling…" : undefined}
              onClick={() => project() && pullGit(project()!)}
            >
              <Show when={syncOp() === "pull"} fallback={<Download class="!size-3.5" />}>
                <Spinner class="size-3.5" />
              </Show>
              Pull
              <AheadBehindPill kind="behind" count={behindCount()} upstream={upstreamName()} class="ml-1.5" />
            </Button>
            <Button
              variant="ghost"
              size="xs"
              class="text-muted-foreground"
              disabled={isSyncing()}
              title={syncOp() === "push" ? "Pushing…" : undefined}
              onClick={() => project() && pushGit(project()!)}
            >
              <Show when={syncOp() === "push"} fallback={<Upload class="!size-3.5" />}>
                <Spinner class="size-3.5" />
              </Show>
              Push
              <AheadBehindPill kind="ahead" count={aheadCount()} upstream={upstreamName()} class="ml-1.5" />
            </Button>
          </div>

          {/* Mobile Actions Dropdown */}
          <div class="md:hidden">
            <DropdownMenu>
              <DropdownMenuTrigger
                as={Button}
                variant="ghost"
                size="icon-sm"
                class="text-muted-foreground"
                aria-label="Git operations"
              >
                <MoreHorizontal class="!size-4" />
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem disabled={isSyncing()} onSelect={() => project() && fetchGit(project()!)}>
                  <Show when={syncOp() === "fetch"} fallback={<CloudDownload class="size-3.5 mr-2" />}>
                    <Spinner class="size-3.5 mr-2" />
                  </Show>
                  Fetch
                </DropdownMenuItem>
                <DropdownMenuItem disabled={isSyncing()} onSelect={() => project() && pullGit(project()!)}>
                  <Show when={syncOp() === "pull"} fallback={<Download class="size-3.5 mr-2" />}>
                    <Spinner class="size-3.5 mr-2" />
                  </Show>
                  Pull
                  <AheadBehindPill kind="behind" count={behindCount()} upstream={upstreamName()} class="ml-auto" />
                </DropdownMenuItem>
                <DropdownMenuItem disabled={isSyncing()} onSelect={() => project() && pushGit(project()!)}>
                  <Show when={syncOp() === "push"} fallback={<Upload class="size-3.5 mr-2" />}>
                    <Spinner class="size-3.5 mr-2" />
                  </Show>
                  Push
                  <AheadBehindPill kind="ahead" count={aheadCount()} upstream={upstreamName()} class="ml-auto" />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>

          <Button
            variant="ghost"
            size="icon-sm"
            class="text-muted-foreground"
            onClick={() => project() && loadGitLog(project()!)}
            title="Refresh Git log"
          >
            <RefreshCw class="!size-3.5" />
          </Button>

          {/* Desktop Panel Toggles */}
          <div class="hidden lg:flex items-center gap-0.5 pl-1 border-l ml-1">
            <Tooltip>
              <TooltipTrigger
                as={Button}
                variant={showBranchesPane() ? "secondary" : "ghost"}
                size="icon-sm"
                class={cn("text-muted-foreground size-7", showBranchesPane() && "text-foreground bg-accent")}
                onClick={() => setShowBranchesPane(!showBranchesPane())}
              >
                <PanelLeft class="!size-3.5" />
                <span class="sr-only">Toggle branches pane</span>
              </TooltipTrigger>
              <TooltipContent>{showBranchesPane() ? "Hide branches pane" : "Show branches pane"}</TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                as={Button}
                variant={showFilesPane() ? "secondary" : "ghost"}
                size="icon-sm"
                class={cn("text-muted-foreground size-7", showFilesPane() && "text-foreground bg-accent")}
                onClick={() => setShowFilesPane(!showFilesPane())}
              >
                <PanelRight class="!size-3.5" />
                <span class="sr-only">Toggle changed files pane</span>
              </TooltipTrigger>
              <TooltipContent>{showFilesPane() ? "Hide changed files pane" : "Show changed files pane"}</TooltipContent>
            </Tooltip>
          </div>
        </div>
      </div>

      {/* Mobile Tab Switcher (< lg) */}
      <div class="flex h-9 shrink-0 items-stretch border-b bg-card/30 lg:hidden overflow-x-auto">
        <button
          type="button"
          onClick={() => setMobileTab("commits")}
          class={cn(
            "flex flex-1 min-w-[75px] items-center justify-center gap-1.5 px-2 text-xs transition-colors border-b-2",
            mobileTab() === "commits"
              ? "border-primary font-semibold text-primary bg-primary/5"
              : "border-transparent text-muted-foreground hover:text-foreground hover:bg-muted/40"
          )}
        >
          <GitCommitHorizontal class="size-3.5 shrink-0" />
          <span>Commits</span>
          <span class="rounded-full bg-muted px-1.5 py-px text-[10px] font-mono tabular opacity-80">
            {filteredCommits().length}
          </span>
        </button>

        <button
          type="button"
          onClick={() => setMobileTab("diff")}
          class={cn(
            "flex flex-1 min-w-[65px] items-center justify-center gap-1.5 px-2 text-xs transition-colors border-b-2",
            mobileTab() === "diff"
              ? "border-primary font-semibold text-primary bg-primary/5"
              : "border-transparent text-muted-foreground hover:text-foreground hover:bg-muted/40"
          )}
        >
          <FileText class="size-3.5 shrink-0" />
          <span>Diff</span>
          <Show when={currentCommitHash()}>
            <span class="rounded bg-muted px-1 py-px text-[10px] font-mono tabular opacity-80 truncate max-w-[60px]">
              {isWorkdir() ? "work" : currentCommitHash()!.substring(0, 6)}
            </span>
          </Show>
        </button>

        <button
          type="button"
          onClick={() => setMobileTab("files")}
          class={cn(
            "flex flex-1 min-w-[65px] items-center justify-center gap-1.5 px-2 text-xs transition-colors border-b-2",
            mobileTab() === "files"
              ? "border-primary font-semibold text-primary bg-primary/5"
              : "border-transparent text-muted-foreground hover:text-foreground hover:bg-muted/40"
          )}
        >
          <FileCode class="size-3.5 shrink-0" />
          <span>Files</span>
          <Show when={allFiles().length > 0}>
            <span class="rounded-full bg-muted px-1.5 py-px text-[10px] font-mono tabular opacity-80">
              {allFiles().length}
            </span>
          </Show>
        </button>

        <button
          type="button"
          onClick={() => setMobileTab("branches")}
          class={cn(
            "flex flex-1 min-w-[80px] items-center justify-center gap-1.5 px-2 text-xs transition-colors border-b-2",
            mobileTab() === "branches"
              ? "border-primary font-semibold text-primary bg-primary/5"
              : "border-transparent text-muted-foreground hover:text-foreground hover:bg-muted/40"
          )}
        >
          <GitBranch class="size-3.5 shrink-0" />
          <span>Branches</span>
          <span class="rounded-full bg-muted px-1.5 py-px text-[10px] font-mono tabular opacity-80">
            {branches().length}
          </span>
        </button>
      </div>

      {/* Main Content Area */}
      <div class="relative min-h-0 flex-1">
        {/* Desktop / Laptop Split View (>= lg) */}
        <div class="hidden lg:flex h-full w-full divide-x">
          {/* Column 1: Refs sidebar */}
          <Show when={showBranchesPane()}>
            <div class="w-48 shrink-0 overflow-hidden">
              {renderRefsSidebar()}
            </div>
          </Show>

          {/* Column 2: Commit log */}
          <div
            class={cn(
              "shrink-0 overflow-hidden",
              showBranchesPane() ? "w-1/3 min-w-[280px] max-w-[400px]" : "w-1/3 min-w-[300px] max-w-[460px]"
            )}
          >
            {renderCommitList()}
          </div>

          {/* Column 3: Diff area */}
          <div class="min-w-0 flex-1 overflow-hidden">
            {renderDiffArea()}
          </div>

          {/* Column 4: Changed files */}
          <Show when={showFilesPane() && currentCommitHash() && diffResult()}>
            <div class="w-60 min-w-[200px] shrink-0 overflow-hidden">
              {renderFilesList()}
            </div>
          </Show>
        </div>

        {/* Mobile Tab View (< lg) */}
        <div class="h-full w-full overflow-hidden lg:hidden">
          <Show when={mobileTab() === "commits"}>
            {renderCommitList()}
          </Show>
          <Show when={mobileTab() === "diff"}>
            {renderDiffArea()}
          </Show>
          <Show when={mobileTab() === "files"}>
            {renderFilesList()}
          </Show>
          <Show when={mobileTab() === "branches"}>
            {renderRefsSidebar()}
          </Show>
        </div>
      </div>
    </div>
  );
}

// --- internal components -----------------------------------------------------

function RefSection(props: { icon: import("solid-js").JSX.Element; label: string; open: boolean; onToggle: () => void; children: import("solid-js").JSX.Element }) {
  return (
    <div class="flex flex-col">
      <button
        type="button"
        onClick={props.onToggle}
        class="flex items-center justify-between bg-muted/30 px-3 py-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground transition-colors hover:text-foreground"
      >
        <span class="flex min-w-0 items-center gap-1.5">
          {props.icon}
          <span class="truncate normal-case">{props.label}</span>
        </span>
        {props.open ? <ChevronDown class="size-3 shrink-0" /> : <ChevronRight class="size-3 shrink-0" />}
      </button>
      <Show when={props.open}>
        <div class="flex flex-col pb-1 pt-0.5">{props.children}</div>
      </Show>
    </div>
  );
}

function RefRow(props: { name: string; hash: string; active?: boolean; badge?: string; ahead?: number; behind?: number; upstream?: string; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onSelect}
      title={`${props.name} (${props.hash.substring(0, 7)})${props.upstream ? ` → ${props.upstream}` : ""}`}
      class={cn(
        "flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-[11px] transition-colors",
        props.active ? "bg-success/10 font-medium text-success" : "hover:bg-muted/60"
      )}
    >
      <span class="flex min-w-0 items-center gap-1.5">
        <GitBranch class={cn("size-2.5 shrink-0", props.active ? "text-success" : "text-muted-foreground")} />
        <span class="truncate font-mono">{props.name}</span>
      </span>
      <span class="flex shrink-0 items-center gap-1">
        <Show when={(props.ahead ?? 0) > 0}>
          <span class="font-mono text-[10px] tabular text-success">↑{props.ahead}</span>
        </Show>
        <Show when={(props.behind ?? 0) > 0}>
          <span class="font-mono text-[10px] tabular text-warning">↓{props.behind}</span>
        </Show>
        <Show when={props.badge}>
          <Badge class="border-success/30 bg-success/15 px-1 text-[9px] text-success">{props.badge}</Badge>
        </Show>
      </span>
    </button>
  );
}

function CopyHash(props: { hash: string }) {
  const [copied, setCopied] = createSignal(false);
  return (
    <Button
      variant="outline"
      size="sm"
      class="h-6 shrink-0 gap-1 px-2 font-mono text-[11px]"
      onClick={() => {
        if (!props.hash) return;
        void navigator.clipboard.writeText(props.hash).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        });
      }}
    >
      <Show when={copied()} fallback={<Copy class="size-3" />}>
        <Check class="size-3 text-success" />
      </Show>
      {props.hash.substring(0, 7)}
    </Button>
  );
}

function FileRowAll(props: { count: number; additions?: number; deletions?: number; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onSelect}
      class={cn(
        "flex w-full items-center justify-between px-3 py-2 text-left text-[11px]",
        props.selected ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground hover:bg-muted/60"
      )}
    >
      <span class="flex items-center gap-1.5">
        <FileCode class="size-3.5" />
        All files ({props.count})
      </span>
      <Show when={(props.additions ?? 0) > 0 || (props.deletions ?? 0) > 0}>
        <span class="flex shrink-0 items-center gap-1 font-mono text-[10px] tabular">
          <Show when={(props.additions ?? 0) > 0}>
            <span class="text-success">+{(props.additions ?? 0)}</span>
          </Show>
          <Show when={(props.deletions ?? 0) > 0}>
            <span class="text-destructive">−{(props.deletions ?? 0)}</span>
          </Show>
        </span>
      </Show>
    </button>
  );
}

function FileGroup(props: {
  label: string;
  tone: "staged" | "unstaged";
  actionLabel: string;
  onAction: () => void;
  files: GitFileChange[];
  selectedPath: string | null;
  onSelect: (f: GitFileChange) => void;
  onFileAction: (f: GitFileChange) => void;
  fileActionLabel: string;
}) {
  return (
    <Show when={props.files.length > 0}>
      <div
        class={cn(
          "flex items-center justify-between border-y px-3 py-1.5 text-[10.5px] font-semibold",
          props.tone === "staged" ? "border-success/20 bg-success/8 text-success" : "border-warning/20 bg-warning/8 text-warning"
        )}
      >
        <span>{props.label}</span>
        <Button
          variant="ghost"
          size="xs"
          class="h-5 gap-0.5 px-1.5 font-mono text-[10px] hover:bg-background/60"
          onClick={(e) => {
            e.stopPropagation();
            props.onAction();
          }}
        >
          {props.tone === "staged" ? <Minus class="size-2.5" /> : <Plus class="size-2.5" />}
          {props.actionLabel}
        </Button>
      </div>
      <For each={props.files}>
        {(file) => (
          <FileRow
            file={file}
            selected={props.selectedPath === file.path}
            onSelect={() => props.onSelect(file)}
            actionLabel={props.fileActionLabel}
            onAction={() => props.onFileAction(file)}
          />
        )}
      </For>
    </Show>
  );
}

function FileRow(props: { file: GitFileChange; selected: boolean; onSelect: () => void; actionLabel?: string; onAction?: () => void }) {
  const badge = statusBadge(props.file.status);
  return (
    <div
      onClick={props.onSelect}
      role="presentation"
      class={cn(
        "group flex cursor-pointer select-none items-center justify-between gap-2 px-3 py-2 text-left text-[11px] transition-colors",
        props.selected ? "bg-accent text-accent-foreground" : "hover:bg-muted/60"
      )}
    >
      <div class="flex min-w-0 flex-1 items-center gap-1.5">
        <span class={cn("shrink-0 rounded border px-1 font-mono text-[9.5px] font-semibold", badge.class)}>
          {badge.label}
        </span>
        <span class="truncate font-mono" title={props.file.oldPath ? `${props.file.oldPath} → ${props.file.path}` : props.file.path}>
          {props.file.oldPath && props.file.oldPath !== props.file.path
            ? `${props.file.oldPath} → ${props.file.path}`
            : props.file.path}
        </span>
      </div>
      <div class="flex shrink-0 items-center gap-1 font-mono text-[10px] tabular">
        <Show when={props.file.additions > 0}>
          <span class="text-success">+{props.file.additions}</span>
        </Show>
        <Show when={props.file.deletions > 0}>
          <span class="text-destructive">−{props.file.deletions}</span>
        </Show>
        <Show when={props.actionLabel && props.onAction}>
          <Button
            variant="ghost"
            size="icon-xs"
            class="ml-1 size-5 rounded p-0 text-muted-foreground opacity-100 lg:opacity-0 lg:group-hover:opacity-100"
            title={`${props.actionLabel} ${props.file.path}`}
            onClick={(e) => {
              e.stopPropagation();
              props.onAction?.();
            }}
          >
            {props.actionLabel === "Unstage" ? <Minus class="size-3" /> : <Plus class="size-3" />}
          </Button>
        </Show>
      </div>
    </div>
  );
}

// --- diff rendering -----------------------------------------------------------

const CONTEXT_STEP = 20;

function DiffBody(props: { project: string; hash: string; diff: string; files?: GitFileChange[]; selectedFile: string | null; loading?: boolean }) {
  const [contextLines, setContextLines] = createSignal(3);
  const [collapsedFiles, setCollapsedFiles] = createSignal<Record<string, boolean>>({});

  createEffect(() => {
    props.hash;
    setContextLines(3);
    setCollapsedFiles({});
  });

  const fileStatsMap = createMemo(() => {
    const map = new Map<string, GitFileChange>();
    for (const f of props.files ?? []) {
      map.set(f.path, f);
    }
    return map;
  });

  const chunks = createMemo(() => parseDiff(props.diff));
  const visibleChunks = createMemo(() =>
    props.selectedFile ? chunks().filter((c) => c.filePath === props.selectedFile) : chunks()
  );

  const toggleFile = (path: string) =>
    setCollapsedFiles((prev) => ({ ...prev, [path]: !prev[path] }));

  const setAllCollapsed = (collapsed: boolean) =>
    setCollapsedFiles(Object.fromEntries(visibleChunks().map((c) => [c.filePath, collapsed])));

  const expandTo = (n: number) => {
    setContextLines(n);
    loadGitDiff(props.project, props.hash, n === 3 ? 3 : Math.min(n, 100000), true);
  };

  return (
    <div class="flex flex-col gap-3 sm:gap-4 max-w-full">
      <Show when={visibleChunks().length > 1}>
        <div class="flex shrink-0 items-center justify-end gap-1">
          <Button
            variant="ghost"
            size="xs"
            class="h-5 px-1.5 text-[10px] text-muted-foreground"
            onClick={() => setAllCollapsed(true)}
          >
            Collapse all
          </Button>
          <Button
            variant="ghost"
            size="xs"
            class="h-5 px-1.5 text-[10px] text-muted-foreground"
            onClick={() => setAllCollapsed(false)}
          >
            Expand all
          </Button>
        </div>
      </Show>
      <For each={visibleChunks()}>
        {(chunk) => {
          const meta = parseGitMeta(chunk.metaLines);
          const fileStat = () => fileStatsMap().get(chunk.filePath);
          const collapsed = () => !!collapsedFiles()[chunk.filePath];
          const rename = () => {
            const fs = fileStat();
            return fs && fs.oldPath && fs.oldPath !== fs.path ? `${fs.oldPath} → ${fs.path}` : null;
          };
          return (
            <Card class="overflow-hidden p-0 shadow-sm max-w-full">
              <div
                onClick={() => toggleFile(chunk.filePath)}
                role="presentation"
                class="sticky top-0 z-10 flex cursor-pointer select-none items-center justify-between gap-2 border-b bg-muted/70 px-1.5 sm:px-2 py-1 backdrop-blur"
                title={collapsed() ? "Expand file diff" : "Collapse file diff"}
              >
                <div class="flex min-w-0 items-center gap-2">
                  {collapsed() ? (
                    <ChevronRight class="size-3.5 shrink-0 text-muted-foreground" />
                  ) : (
                    <ChevronDown class="size-3.5 shrink-0 text-muted-foreground" />
                  )}
                  <FileCode class="size-3.5 shrink-0 text-primary" />
                  <span class="min-w-0 truncate font-mono text-[11px] font-medium">
                    {rename() ?? chunk.filePath}
                  </span>
                  <Show when={fileStat()}>
                    {(stat) => (
                      <Show when={stat().additions > 0 || stat().deletions > 0}>
                        <span class="flex shrink-0 items-center gap-1 font-mono text-[10px] tabular">
                          <Show when={stat().additions > 0}>
                            <span class="text-success">+{stat().additions}</span>
                          </Show>
                          <Show when={stat().deletions > 0}>
                            <span class="text-destructive">−{stat().deletions}</span>
                          </Show>
                        </span>
                      </Show>
                    )}
                  </Show>
                </div>
                <div class="flex shrink-0 items-center gap-1.5 text-[10px]">
                  <Show when={meta.blobs}>
                    <span class="rounded border border-border/70 bg-muted/60 px-1.5 py-px font-mono text-muted-foreground">
                      {meta.blobs!.oldHash.substring(0, 7)} → {meta.blobs!.newHash.substring(0, 7)}
                    </span>
                  </Show>
                  <Show when={meta.modeChange}>
                    <span class="rounded bg-warning/15 px-1.5 py-px text-warning">
                      mode {meta.modeChange!.oldMode} → {meta.modeChange!.newMode}
                    </span>
                  </Show>
                  <Show when={meta.isExecutable}>
                    <span class="rounded bg-info/15 px-1.5 py-px text-info">exec</span>
                  </Show>
                  <Show when={contextLines() > 3}>
                    <Button
                      variant="ghost"
                      size="xs"
                      disabled={props.loading}
                      class="h-5 px-1.5 text-[10px] text-muted-foreground"
                      onClick={(e) => {
                        e.stopPropagation();
                        expandTo(3);
                      }}
                    >
                      reset ctx
                    </Button>
                  </Show>
                </div>
              </div>

              <Show when={!collapsed()}>

                <div class="w-full overflow-x-auto">
                  <table class="w-full border-collapse">
                    <tbody>
                      <For each={chunk.lines}>
                        {(line) => {
                          if (line.type === "hunk") {
                            return (
                              <tr class="border-y border-primary/15 bg-primary/6 select-none">
                                <td class="w-8 md:w-9 border-r border-border/60" />
                                <td class="w-8 md:w-9 border-r border-border/60" />
                                <td class="px-2 sm:px-3 py-0.5">
                                  <div class="flex flex-wrap items-center justify-between gap-1.5">
                                    <span class="truncate font-medium text-primary/90 text-[10.5px] sm:text-[11px]">{line.text}</span>
                                    <span class="flex shrink-0 items-center gap-1">
                                      <Button
                                        variant="secondary"
                                        size="xs"
                                        disabled={props.loading}
                                        class="h-5 bg-primary/12 px-1.5 text-[10px] font-medium text-primary hover:bg-primary/20"
                                        onClick={() => expandTo(contextLines() + CONTEXT_STEP)}
                                      >
                                        +{CONTEXT_STEP} ctx
                                      </Button>
                                      <Button
                                        variant="secondary"
                                        size="xs"
                                        disabled={props.loading}
                                        class="h-5 bg-primary/12 px-1.5 text-[10px] font-medium text-primary hover:bg-primary/20"
                                        onClick={() => expandTo(100000)}
                                      >
                                        full
                                      </Button>
                                    </span>
                                  </div>
                                </td>
                              </tr>
                            );
                          }
                          if (line.type === "note") {
                            return (
                              <tr class="select-none bg-muted/20">
                                <td class="w-8 md:w-9 border-r border-border/60" />
                                <td class="w-8 md:w-9 border-r border-border/60" />
                                <td class="px-2 sm:px-3 py-0.5 font-mono text-[10px] italic text-muted-foreground/70">{line.text}</td>
                              </tr>
                            );
                          }
                          const tone =
                            line.type === "add"
                              ? "bg-success/8 text-success"
                              : line.type === "delete"
                                ? "bg-destructive/8 text-destructive"
                                : "";
                          return (
                            <tr class={cn("transition-colors hover:bg-muted/25", tone)}>
                              <td class="w-8 md:w-9 select-none border-r border-border/50 pr-1 text-right align-top font-mono text-[9px] md:text-[9.5px] tabular text-muted-foreground/50">
                                {line.oldLine ?? ""}
                              </td>
                              <td class="w-8 md:w-9 select-none border-r border-border/50 pr-1 text-right align-top font-mono text-[9px] md:text-[9.5px] tabular text-muted-foreground/50">
                                {line.newLine ?? ""}
                              </td>
                              <td class="whitespace-pre px-2 sm:px-3 font-mono text-[11px] sm:text-[11.5px]">{line.text || " "}</td>
                            </tr>
                          );
                        }}
                      </For>
                    </tbody>
                  </table>
                </div>
              </Show>
            </Card>
          );
        }}
      </For>
    </div>
  );
}
