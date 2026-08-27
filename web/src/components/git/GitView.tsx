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
  GitBranch,
  GitCommitHorizontal,
  Loader2,
  Maximize2,
  Minus,
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
import { closeGitView, selectedProject } from "~/stores/nav";
import type { GitBranch as GitBranchType, GitFileChange } from "~/lib/types";
import { computeGitGraph, GRAPH_COLORS } from "~/lib/git_graph";
import { parseDiff, parseGitMeta } from "~/lib/diff";
import { formatAuthorTime, formatRelativeTime } from "~/lib/format";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Textarea } from "~/components/ui/textarea";
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

function RefBadge(props: { ref: { name: string; type: string; is_active?: boolean }; hasActiveBranch: boolean }) {
  const r = () => props.ref;
  if (r().type === "branch" && r().is_active) {
    return (
      <Badge class="border-success/40 bg-success/15 font-mono text-[10px] text-success">
        <GitBranch class="size-2.5" />
        {r().name}
        <Check class="size-2.5 stroke-[3]" />
      </Badge>
    );
  }
  if (r().type === "branch") {
    return (
      <Badge class="border-primary/30 bg-primary/10 font-mono text-[10px] text-primary">
        <GitBranch class="size-2.5" />
        {r().name}
      </Badge>
    );
  }
  if (r().type === "remote") {
    return (
      <Badge variant="outline" class="font-mono text-[10px]">
        origin/{r().name}
      </Badge>
    );
  }
  if (r().type === "tag") {
    return (
      <Badge class="border-warning/40 bg-warning/15 font-mono text-[10px] text-warning">
        <Tag class="size-2.5" />
        {r().name}
      </Badge>
    );
  }
  if (r().type === "stash") {
    return (
      <Badge variant="secondary" class="font-mono text-[10px]">
        <Archive class="size-2.5" />
        {r().name}
      </Badge>
    );
  }
  if (r().type === "head" && !props.hasActiveBranch) {
    return <Badge variant="secondary" class="font-mono text-[10px]">HEAD</Badge>;
  }
  return null;
}

export function GitView() {
  const project = () => selectedProject();
  const commits = createMemo(() => (project() ? gitCommits()[project()!] ?? [] : []));
  const branches = createMemo(() => (project() ? gitBranches()[project()!] ?? [] : []));
  const tags = createMemo(() => (project() ? gitTags()[project()!] ?? [] : []));
  const stashes = createMemo(() => (project() ? gitStashes()[project()!] ?? [] : []));

  const activeBranch = createMemo(() => branches().find((b) => b.is_active));
  const localBranches = createMemo(() => branches().filter((b) => !b.is_remote));
  const remoteBranches = createMemo(() => branches().filter((b) => b.is_remote));
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

  const error = createMemo(() => (project() ? gitError()[project()!] ?? "" : ""));
  const loading = createMemo(() => (project() ? !!gitLoading()[project()!] : false));

  const currentCommitHash = createMemo(() => (project() ? selectedCommitHash()[project()!] ?? null : null));
  const currentFilePath = createMemo(() => (project() ? selectedFilePath()[project()!] ?? null : null));

  const [searchQuery, setSearchQuery] = createSignal("");
  const [fileSearchQuery, setFileSearchQuery] = createSignal("");
  const [commitMessage, setCommitMessage] = createSignal("");

  const isCommitting = createMemo(() => (project() ? !!gitCommitLoading()[project()!] : false));
  const commitErr = createMemo(() => (project() ? gitCommitError()[project()!] ?? "" : ""));

  const graphMap = createMemo(() => computeGitGraph(commits()));
  const maxColumns = createMemo(() => {
    let max = 1;
    for (const info of graphMap().values()) {
      if (info.activeCount > max) max = info.activeCount;
    }
    return Math.min(max, 8);
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

  return (
    <div class="flex h-full min-w-0 flex-col overflow-hidden bg-background">
      {/* Top bar */}
      <div class="flex h-9 shrink-0 items-center justify-between gap-2 border-b bg-card/50 px-2">
        <div class="flex min-w-0 items-center gap-1.5">
          <Button variant="ghost" size="xs" class="gap-1 text-muted-foreground hover:text-foreground" onClick={() => closeGitView()}>
            <ArrowLeft class="!size-3.5" />
            Back
          </Button>
          <div class="mx-1 h-4 w-px bg-border" />
          <GitBranch class="size-3.5 shrink-0 text-primary" />
          <span class="truncate text-[13px] font-semibold">{project()}</span>
          <Show when={activeBranch()}>
            {(b) => (
              <div class="flex shrink-0 items-center gap-1">
                <Badge class="border-success/35 bg-success/12 font-mono text-[10px] text-success">
                  {b().name}
                </Badge>
                <Show when={(b().ahead ?? 0) > 0}>
                  <span class="rounded-full bg-success/12 px-1.5 py-px font-mono text-[10px] tabular text-success" title={`${b().ahead} ahead of ${b().upstream}`}>
                    ↑{b().ahead}
                  </span>
                </Show>
                <Show when={(b().behind ?? 0) > 0}>
                  <span class="rounded-full bg-warning/12 px-1.5 py-px font-mono text-[10px] tabular text-warning" title={`${b().behind} behind ${b().upstream}`}>
                    ↓{b().behind}
                  </span>
                </Show>
              </div>
            )}
          </Show>
        </div>

        <div class="flex shrink-0 items-center gap-1">
          <Input
            type="text"
            placeholder="Search commits…"
            value={searchQuery()}
            onInput={(e) => setSearchQuery(e.currentTarget.value)}
            class="h-6.5 w-44 rounded-md bg-background px-2 text-xs md:text-xs sm:w-56"
          />
          <Button variant="ghost" size="xs" class="text-muted-foreground hover:text-foreground" onClick={() => project() && fetchGit(project()!)}>
            <CloudDownload class="!size-3.5" /> Fetch
          </Button>
          <Button variant="ghost" size="xs" class="text-muted-foreground hover:text-foreground" onClick={() => project() && pullGit(project()!)}>
            <Download class="!size-3.5" /> Pull
          </Button>
          <Button variant="ghost" size="xs" class="text-muted-foreground hover:text-foreground" onClick={() => project() && pushGit(project()!)}>
            <Upload class="!size-3.5" /> Push
          </Button>
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground hover:text-foreground" onClick={() => project() && loadGitLog(project()!)} title="Refresh">
            <RefreshCw class="!size-3.5" />
          </Button>
        </div>
      </div>

      {/* Body */}
      <div class="flex min-h-0 flex-1 divide-x">
        {/* Refs sidebar */}
        <div class="flex w-48 shrink-0 select-none flex-col divide-y overflow-y-auto bg-card/40">
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
                  active={!!b.is_active}
                  badge={b.is_active ? "HEAD" : undefined}
                  ahead={b.ahead}
                  behind={b.behind}
                  upstream={b.upstream}
                  onSelect={() => project() && selectCommit(project()!, b.hash)}
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
            <Show when={remoteNames().length > 0} fallback={<p class="px-3 py-1 text-[11px] italic text-muted-foreground">No remotes</p>}>
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
                        class="flex w-full items-center justify-between gap-2 px-3 py-1 text-left text-[11px] text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
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
                              onClick={() => project() && selectCommit(project()!, b.hash)}
                              title={`${b.name} (${b.hash.substring(0, 7)})`}
                              class="w-full truncate py-1 pl-7 pr-3 text-left font-mono text-[11px] text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
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
            <Show when={tags().length > 0} fallback={<p class="px-3 py-1 text-[11px] italic text-muted-foreground">No tags</p>}>
              <For each={tags()}>
                {(t) => (
                  <RefRow name={t.name} hash={t.hash} onSelect={() => project() && selectCommit(project()!, t.hash)} />
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
            <Show when={stashes().length > 0} fallback={<p class="px-3 py-1 text-[11px] italic text-muted-foreground">No stashes</p>}>
              <For each={stashes()}>
                {(s) => (
                  <button
                    type="button"
                    onClick={() => project() && selectCommit(project()!, s.hash)}
                    title={`${s.index}: ${s.name}`}
                    class="flex w-full min-w-0 flex-col gap-0.5 px-3 py-1 text-left text-xs transition-colors hover:bg-muted/60"
                  >
                    <span class="truncate font-mono text-[11px]">{s.index}</span>
                    <span class="truncate pl-0.5 text-[10px] leading-tight text-muted-foreground">{s.name}</span>
                  </button>
                )}
              </For>
            </Show>
          </RefSection>
        </div>

        {/* Commit log */}
        <div class="flex w-1/3 min-w-[300px] max-w-[440px] shrink-0 flex-col bg-background">
          <div class="flex h-7 shrink-0 items-center border-b bg-muted/20 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
            Commits <span class="ml-1.5 tabular">{filteredCommits().length}</span>
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
              when={!loading()}
              fallback={
                <div class="flex h-full items-center justify-center gap-2 p-6 text-muted-foreground">
                  <Loader2 class="size-4 animate-spin" />
                  <span class="text-xs">Loading commits…</span>
                </div>
              }
            >
              <Show
                when={!error()}
                fallback={
                  <div class="p-4 text-center">
                    <p class="mb-2 break-words text-xs text-destructive">{error()}</p>
                    <Button variant="outline" size="xs" onClick={() => project() && loadGitLog(project()!)}>
                      Retry
                    </Button>
                  </div>
                }
              >
                <Show
                  when={filteredCommits().length > 0}
                  fallback={
                    <div class="p-6 text-center text-xs text-muted-foreground">
                      {commits().length === 0 ? "No commits in repository." : "No matching commits."}
                    </div>
                  }
                >
                  <For each={filteredCommits()}>
                    {(commit) => {
                      const isSelected = () => currentCommitHash() === commit.hash;
                      const isWorkdirRow = commit.hash === "WORKDIR";
                      const colWidth = 13;
                      const rowHeight = 46;
                      const info = () => graphMap().get(commit.hash);

                      return (
                        <button
                          type="button"
                          data-commit={commit.hash}
                          onClick={() => project() && selectCommit(project()!, commit.hash)}
                          class={cn(
                            "relative flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors",
                            isSelected()
                              ? "bg-accent"
                              : isWorkdirRow
                                ? "hover:bg-warning/8"
                                : "hover:bg-muted/50"
                          )}
                        >
                          {/* selection bar */}
                          <span
                            class={cn(
                              "absolute left-0 top-0 h-full w-[2.5px]",
                              isSelected() ? "bg-primary" : "bg-transparent"
                            )}
                          />
                          {/* DAG column */}
                          <div class="relative h-[46px] shrink-0 self-stretch" style={{ width: `${maxColumns() * colWidth}px` }}>
                            <svg class="pointer-events-none absolute inset-0 h-full w-full">
                              <For each={info()?.connections ?? []}>
                                {(conn) => {
                                  const x1 = conn.fromColumn * colWidth + colWidth / 2;
                                  const x2 = conn.toColumn * colWidth + colWidth / 2;
                                  const color = isWorkdirRow ? "#fbbf24" : GRAPH_COLORS[conn.colorIndex % GRAPH_COLORS.length];
                                  if (conn.fromColumn === conn.toColumn) {
                                    return <line x1={x1} y1={0} x2={x2} y2={rowHeight} stroke={color} stroke-width="1.75" />;
                                  }
                                  const y1 = rowHeight / 2;
                                  const y2 = rowHeight;
                                  return (
                                    <path
                                      d={`M ${x1} ${y1} C ${x1} ${(y1 + y2) / 2}, ${x2} ${(y1 + y2) / 2}, ${x2} ${y2}`}
                                      fill="none"
                                      stroke={color}
                                      stroke-width="1.75"
                                    />
                                  );
                                }}
                              </For>
                              <Show when={info()} keyed>
                                {(i) => (
                                  <circle
                                    cx={i.column * colWidth + colWidth / 2}
                                    cy={rowHeight / 2}
                                    r={isSelected() ? 4.5 : 3.5}
                                    fill={isWorkdirRow ? "#fbbf24" : GRAPH_COLORS[i.colorIndex % GRAPH_COLORS.length]}
                                    stroke="var(--background)"
                                    stroke-width="1.5"
                                  />
                                )}
                              </Show>
                            </svg>
                          </div>

                          <div class="flex min-w-0 flex-1 flex-col gap-0.5">
                            <div class="flex min-w-0 flex-wrap items-center gap-1">
                              <Show when={isWorkdirRow}>
                                <Badge class="border-warning/40 bg-warning/15 text-[10px] text-warning">Uncommitted</Badge>
                              </Show>
                              <Show when={commit.refs && commit.refs.length > 0}>
                                <For each={commit.refs}>
                                  {(ref) => <RefBadge ref={ref} hasActiveBranch={!!commit.refs?.some((r) => r.type === "branch" && r.is_active)} />}
                                </For>
                              </Show>
                              <span class={cn("truncate leading-tight", isWorkdirRow ? "font-semibold text-warning" : "")}>
                                {commit.subject}
                              </span>
                            </div>
                            <div class="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                              <span class="truncate">{commit.author}</span>
                              <span class="shrink-0 opacity-50">·</span>
                              <span class="shrink-0">{formatRelativeTime(commit.time)}</span>
                              <Show when={!isWorkdirRow}>
                                <span class="ml-auto shrink-0 font-mono text-[10px] opacity-60">{commit.short}</span>
                              </Show>
                            </div>
                          </div>
                        </button>
                      );
                    }}
                  </For>
                </Show>
              </Show>
            </Show>
          </div>
        </div>

        {/* Diff area */}
        <div class="flex min-w-0 flex-1 flex-col overflow-hidden bg-background">
          <Show
            when={currentCommitHash()}
            fallback={
              <div class="flex h-full flex-col items-center justify-center gap-2 p-6 text-muted-foreground">
                <GitCommitHorizontal class="size-8 stroke-1" />
                <p class="text-sm">Select a commit to view its diff.</p>
              </div>
            }
          >
            <Show when={!diffLoading() || !!diffResult()} fallback={
              <div class="flex h-full items-center justify-center gap-2 text-muted-foreground">
                <Loader2 class="size-4 animate-spin" /><span class="text-xs">Loading diff…</span>
              </div>
            }>
              {/* Commit meta / worktree form */}
              <div class="flex shrink-0 flex-col gap-2 border-b bg-muted/20 p-3">
                <Show
                  when={isWorkdir()}
                  fallback={
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0">
                        <h2 class="truncate text-sm font-semibold leading-snug">{diffResult()?.commit.subject}</h2>
                        <div class="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
                          <span class="font-medium text-foreground">{diffResult()?.commit.author}</span>
                          <span>&lt;{diffResult()?.commit.email}&gt;</span>
                          <span>·</span>
                          <span>{diffResult()?.commit.time ? formatAuthorTime(diffResult()!.commit.time) : ""}</span>
                        </div>
                      </div>
                      <CopyHash hash={diffResult()?.commit.hash ?? ""} />
                    </div>
                  }
                >
                  <form onSubmit={handleCommitSubmit} class="flex flex-col gap-2">
                    <div class="flex items-center justify-between">
                      <div class="flex items-center gap-2">
                        <Badge class="border-warning/40 bg-warning/15 text-warning">Uncommitted changes</Badge>
                        <span class="text-[11px] tabular text-muted-foreground">
                          {stagedFiles().length} staged · {unstagedFiles().length + untrackedFiles().length} unstaged
                        </span>
                      </div>
                    </div>
                    <div class="flex gap-2">
                      <Textarea
                        placeholder="Commit message… (⌘⏎ to commit)"
                        value={commitMessage()}
                        onInput={(e) => setCommitMessage(e.currentTarget.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) handleCommitSubmit(e);
                        }}
                        rows={2}
                        class="min-h-0 flex-1 resize-none rounded-md bg-background p-2 text-xs md:text-xs"
                      />
                      <Button type="submit" size="sm" class="h-auto self-end" disabled={!commitMessage().trim() || isCommitting()}>
                        <Show when={isCommitting()} fallback={<Send class="!size-3.5" />}>
                          <Loader2 class="!size-3.5 animate-spin" />
                        </Show>
                        Commit
                      </Button>
                    </div>
                    <Show when={commitErr()}>
                      <p class="text-xs font-medium text-destructive">{commitErr()}</p>
                    </Show>
                  </form>
                </Show>
              </div>

              {/* Diff body */}
              <div tabIndex={0} data-kbd-ignore class="min-h-0 flex-1 overflow-y-auto p-3 font-mono text-[11.5px] leading-relaxed outline-none">
                <Show
                  when={diffResult()?.diff}
                  fallback={
                    <div class="py-10 text-center text-xs text-muted-foreground">
                      No changes.
                    </div>
                  }
                >
                  <DiffBody
                    project={project()!}
                    hash={currentCommitHash()!}
                    diff={diffResult()!.diff}
                    selectedFile={currentFilePath()}
                    loading={diffLoading()}
                  />
                </Show>
              </div>
            </Show>
          </Show>
        </div>

        {/* Changed files */}
        <Show when={currentCommitHash() && diffResult()}>
          <div class="flex w-60 min-w-[200px] shrink-0 flex-col bg-card/40">
            <div class="flex h-7 shrink-0 items-center justify-between border-b bg-muted/20 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              Files <span class="tabular">{allFiles().length}</span>
            </div>
            <div class="border-b p-1.5">
              <div class="relative">
                <Search class="pointer-events-none absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
                <Input
                  type="text"
                  placeholder="Filter…"
                  value={fileSearchQuery()}
                  onInput={(e) => setFileSearchQuery(e.currentTarget.value)}
                  class="h-6 w-full rounded-md bg-background pl-6 pr-2 text-[11px] md:text-[11px]"
                />
              </div>
            </div>
            <div data-kbd-ignore tabIndex={0} class="min-h-0 flex-1 overflow-y-auto outline-none">
              <FileRowAll
                count={allFiles().length}
                selected={currentFilePath() === null}
                onSelect={() => project() && selectDiffFile(project()!, null)}
              />
              <Show
                when={isWorkdir()}
                fallback={
                  <For each={allFiles().filter((f) => f.path.toLowerCase().includes(fileFilter()))}>
                    {(file) => (
                      <FileRow
                        file={file}
                        selected={currentFilePath() === file.path}
                        onSelect={() => project() && selectDiffFile(project()!, file.path)}
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
                  onSelect={(f) => project() && selectDiffFile(project()!, f.path)}
                  onFileAction={(f) => project() && stageGitFile(project()!, f.path, true)}
                  fileActionLabel="Unstage"
                />
                <FileGroup
                  label={`Unstaged (${unstagedFiles().length})`}
                  tone="unstaged"
                  actionLabel="Stage all"
                  onAction={() => project() && stageGitFile(project()!, "", false, true)}
                  files={[...unstagedFiles(), ...untrackedFiles()]}
                  selectedPath={currentFilePath()}
                  onSelect={(f) => project() && selectDiffFile(project()!, f.path)}
                  onFileAction={(f) => project() && stageGitFile(project()!, f.path, false)}
                  fileActionLabel="Stage"
                />
              </Show>
            </div>
          </div>
        </Show>
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
        class="flex items-center justify-between bg-muted/30 px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground transition-colors hover:text-foreground"
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
        "flex w-full items-center justify-between gap-2 px-3 py-1 text-left text-[11px] transition-colors",
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

function FileRowAll(props: { count: number; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onSelect}
      class={cn(
        "flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-[11px]",
        props.selected ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground hover:bg-muted/60"
      )}
    >
      <FileCode class="size-3" />
      All files ({props.count})
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
          "flex items-center justify-between border-y px-3 py-1 text-[10.5px] font-semibold",
          props.tone === "staged" ? "border-success/20 bg-success/8 text-success" : "border-warning/20 bg-warning/8 text-warning"
        )}
      >
        {props.label}
        <button
          type="button"
          class="flex items-center gap-0.5 rounded px-1 font-mono text-[10px] transition-colors hover:bg-background/60"
          onClick={(e) => {
            e.stopPropagation();
            props.onAction();
          }}
        >
          {props.tone === "staged" ? <Minus class="size-2.5" /> : <Plus class="size-2.5" />}
          {props.actionLabel}
        </button>
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
        "group flex cursor-pointer select-none items-center justify-between gap-2 px-3 py-1.5 text-left text-[11px] transition-colors",
        props.selected ? "bg-accent text-accent-foreground" : "hover:bg-muted/60"
      )}
    >
      <div class="flex min-w-0 flex-1 items-center gap-1.5">
        <span class={cn("shrink-0 rounded border px-1 font-mono text-[9.5px] font-semibold", badge.class)}>
          {badge.label}
        </span>
        <span class="truncate font-mono" title={props.file.path}>
          {props.file.path}
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
          <button
            type="button"
            class="ml-0.5 rounded px-1 text-muted-foreground opacity-0 transition-all hover:bg-muted hover:text-foreground group-hover:opacity-100"
            title={`${props.actionLabel} ${props.file.path}`}
            onClick={(e) => {
              e.stopPropagation();
              props.onAction?.();
            }}
          >
            {props.actionLabel === "Unstage" ? <Minus class="size-2.5" /> : <Plus class="size-2.5" />}
          </button>
        </Show>
      </div>
    </div>
  );
}

// --- diff rendering -----------------------------------------------------------

const CONTEXT_STEP = 20;

function DiffBody(props: { project: string; hash: string; diff: string; selectedFile: string | null; loading?: boolean }) {
  const [contextLines, setContextLines] = createSignal(3);

  createEffect(() => {
    props.hash;
    setContextLines(3);
  });

  const chunks = createMemo(() => parseDiff(props.diff));
  const visibleChunks = createMemo(() =>
    props.selectedFile ? chunks().filter((c) => c.filePath === props.selectedFile) : chunks()
  );

  const expandTo = (n: number) => {
    setContextLines(n);
    loadGitDiff(props.project, props.hash, n === 3 ? 3 : Math.min(n, 100000), true);
  };

  return (
    <div class="flex flex-col gap-4">
      <For each={visibleChunks()}>
        {(chunk) => {
          const meta = parseGitMeta(chunk.metaLines);
          return (
            <div class="overflow-hidden rounded-lg border bg-card shadow-sm">
              <div class="sticky top-0 z-10 flex items-center justify-between gap-2 border-b bg-muted/60 px-3 py-1.5 backdrop-blur">
                <span class="flex min-w-0 items-center gap-1.5 text-[11px] font-medium">
                  <FileCode class="size-3.5 shrink-0 text-primary" />
                  <span class="truncate font-mono">{chunk.filePath}</span>
                </span>
                <Show when={contextLines() > 3}>
                  <button
                    type="button"
                    disabled={props.loading}
                    class="shrink-0 rounded px-1.5 text-[10px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                    onClick={() => expandTo(3)}
                  >
                    collapse context
                  </button>
                </Show>
              </div>

              <Show when={meta.blobs || meta.modeChange || meta.isExecutable}>
                <div class="flex flex-wrap select-text items-center gap-1.5 border-b bg-muted/25 px-3 py-1 text-[10px]">
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
                    <span class="rounded bg-info/15 px-1.5 py-px text-info">executable</span>
                  </Show>
                </div>
              </Show>

              <table class="w-full border-collapse">
                <tbody>
                  <For each={chunk.lines}>
                    {(line) => {
                      if (line.type === "hunk") {
                        return (
                          <tr class="border-y border-primary/15 bg-primary/6 select-none">
                            <td class="w-9 border-r border-border/60" />
                            <td class="w-9 border-r border-border/60" />
                            <td class="px-3 py-0.5">
                              <div class="flex items-center justify-between gap-2">
                                <span class="truncate font-medium text-primary/90">{line.text}</span>
                                <span class="flex shrink-0 items-center gap-1">
                                  <button
                                    type="button"
                                    disabled={props.loading}
                                    class="rounded bg-primary/12 px-1.5 py-px text-[10px] font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-50"
                                    onClick={() => expandTo(contextLines() + CONTEXT_STEP)}
                                  >
                                    +{CONTEXT_STEP} ctx
                                  </button>
                                  <button
                                    type="button"
                                    disabled={props.loading}
                                    class="rounded bg-primary/12 px-1.5 py-px text-[10px] font-medium text-primary transition-colors hover:bg-primary/20 disabled:opacity-50"
                                    onClick={() => expandTo(100000)}
                                  >
                                    full
                                  </button>
                                </span>
                              </div>
                            </td>
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
                          <td class="w-9 select-none border-r border-border/50 pr-1.5 text-right align-top font-mono text-[9.5px] tabular text-muted-foreground/50">
                            {line.oldLine ?? ""}
                          </td>
                          <td class="w-9 select-none border-r border-border/50 pr-1.5 text-right align-top font-mono text-[9.5px] tabular text-muted-foreground/50">
                            {line.newLine ?? ""}
                          </td>
                          <td class="whitespace-pre-wrap break-all px-3 font-mono">{line.text || " "}</td>
                        </tr>
                      );
                    }}
                  </For>
                </tbody>
              </table>
            </div>
          );
        }}
      </For>
    </div>
  );
}
