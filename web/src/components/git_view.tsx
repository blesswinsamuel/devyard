import { For, Show, createEffect, createMemo, createSignal } from "solid-js";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  FileCode,
  FileDiff,
  GitBranch,
  GitCommitIcon,
  Loader2,
  Maximize2,
  Minus,
  Plus,
  RefreshCw,
  Search,
  Send,
} from "lucide-solid";
import {
  selectedProject,
  gitCommits,
  gitError,
  gitLoading,
  selectedCommitHash,
  selectedFilePath,
  gitDiffs,
  gitDiffLoading,
  gitDiffError,
  gitCommitLoading,
  gitCommitError,
  closeGitView,
  loadGitLog,
  selectCommit,
  selectDiffFile,
  commitGitChanges,
  stageGitFile,
  loadGitDiff,
} from "~/store";
import type { GitCommit, GitFileChange } from "~/types";
import { computeGitGraph, GRAPH_COLORS } from "~/lib/git_graph";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";

function formatAuthorTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function formatRelativeTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const now = new Date();
  const diffSec = Math.floor((now.getTime() - d.getTime()) / 1000);
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 2592000) return `${Math.floor(diffSec / 86400)}d ago`;
  return d.toLocaleDateString();
}

function getStatusBadge(status: string) {
  const st = (status || "M").toUpperCase()[0];
  switch (st) {
    case "A":
      return { label: "A", class: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/20" };
    case "D":
      return { label: "D", class: "bg-rose-500/15 text-rose-600 dark:text-rose-400 border-rose-500/20" };
    case "R":
      return { label: "R", class: "bg-sky-500/15 text-sky-600 dark:text-sky-400 border-sky-500/20" };
    default:
      return { label: "M", class: "bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/20" };
  }
}

export function GitView() {
  const project = () => selectedProject();
  const commits = createMemo(() => (project() ? gitCommits()[project()!] ?? [] : []));
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
    return Math.min(max, 10);
  });

  const filteredCommits = createMemo(() => {
    const q = searchQuery().toLowerCase().trim();
    if (!q) return commits();
    return commits().filter(
      (c) =>
        c.subject.toLowerCase().includes(q) ||
        c.author.toLowerCase().includes(q) ||
        c.hash.toLowerCase().includes(q) ||
        c.short.toLowerCase().includes(q)
    );
  });

  createEffect(() => {
    const list = commits();
    const proj = project();
    if (proj && list.length > 0 && !selectedCommitHash()[proj]) {
      selectCommit(proj, list[0].hash, { skipPush: true });
    }
  });

  const selectedDiffResult = createMemo(() => {
    const proj = project();
    const hash = currentCommitHash();
    if (!proj || !hash) return null;
    return gitDiffs()[proj]?.[hash] ?? null;
  });

  const diffLoading = createMemo(() => {
    const proj = project();
    if (!proj) return false;
    return !!gitDiffLoading()[proj];
  });

  const diffError = createMemo(() => {
    const proj = project();
    if (!proj) return "";
    return gitDiffError()[proj] ?? "";
  });

  const isWorkdir = createMemo(() => currentCommitHash() === "WORKDIR");

  // File groupings for WORKDIR (Sublime Merge style)
  const allFiles = createMemo(() => selectedDiffResult()?.files ?? []);
  const fileFilter = createMemo(() => fileSearchQuery().toLowerCase().trim());

  const stagedFiles = createMemo(() =>
    allFiles().filter((f) => f.staged && (!fileFilter() || f.path.toLowerCase().includes(fileFilter())))
  );
  const unstagedFiles = createMemo(() =>
    allFiles().filter((f) => f.unstaged && (!fileFilter() || f.path.toLowerCase().includes(fileFilter())))
  );
  const untrackedFiles = createMemo(() =>
    allFiles().filter((f) => f.untracked && (!fileFilter() || f.path.toLowerCase().includes(fileFilter())))
  );

  const handleCommitSubmit = (e: Event) => {
    e.preventDefault();
    const proj = project();
    const msg = commitMessage().trim();
    if (!proj || !msg || isCommitting()) return;
    commitGitChanges(proj, msg);
    setCommitMessage("");
  };

  return (
    <div class="flex h-full flex-col min-w-0 overflow-hidden bg-background text-foreground">
      {/* Top Bar */}
      <div class="flex h-10 shrink-0 items-center justify-between gap-2 border-b border-border px-4 bg-muted/20">
        <div class="flex items-center gap-2 min-w-0">
          <Button
            variant="ghost"
            size="sm"
            class="h-7 gap-1.5 px-2 text-muted-foreground hover:text-foreground"
            onClick={closeGitView}
          >
            <ArrowLeft class="size-4" />
            <span class="hidden sm:inline">Back</span>
          </Button>
          <div class="h-4 w-[1px] bg-border mx-1" />
          <div class="flex min-w-0 items-center gap-2">
            <GitBranch class="size-4 shrink-0 text-primary" />
            <span class="truncate font-semibold text-sm">{project()}</span>
            <span class="truncate text-xs text-muted-foreground">/ git history</span>
          </div>
        </div>

        {/* Search & Actions */}
        <div class="flex items-center gap-2">
          <div class="relative w-48 sm:w-64">
            <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              placeholder="Search commits..."
              value={searchQuery()}
              onInput={(e) => setSearchQuery(e.currentTarget.value)}
              class="h-7 w-full rounded-md border border-input bg-background pl-8 pr-2 text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </div>
          <Button
            variant="ghost"
            size="icon"
            class="size-7 text-muted-foreground hover:text-foreground"
            onClick={() => project() && loadGitLog(project()!)}
            title="Refresh Git Log"
          >
            <RefreshCw class="size-3.5" />
            <span class="sr-only">Refresh</span>
          </Button>
        </div>
      </div>

      {/* Main 3-Pane Body */}
      <div class="min-h-0 flex-1 flex divide-x divide-border">
        {/* Left Pane: Commit Log List with DAG Graph */}
        <div class="flex flex-col w-1/3 min-w-[300px] max-w-[500px] shrink-0 bg-background">
          <div class="flex h-8 shrink-0 items-center justify-between border-b border-border px-3 text-xs font-semibold text-muted-foreground bg-muted/10">
            <span>Commits ({filteredCommits().length})</span>
          </div>
          <div class="min-h-0 flex-1 overflow-y-auto divide-y divide-border/60">
            <Show
              when={!loading()}
              fallback={
                <div class="flex h-full items-center justify-center gap-2 text-muted-foreground p-6">
                  <Loader2 class="size-4 animate-spin" />
                  <span class="text-xs">Loading commits…</span>
                </div>
              }
            >
              <Show
                when={!error()}
                fallback={
                  <div class="p-4 text-center">
                    <p class="text-xs text-destructive mb-2">{error()}</p>
                    <Button variant="outline" size="sm" onClick={() => project() && loadGitLog(project()!)}>
                      Retry
                    </Button>
                  </div>
                }
              >
                <Show
                  when={filteredCommits().length > 0}
                  fallback={
                    <div class="p-6 text-center text-xs text-muted-foreground">No matching commits found.</div>
                  }
                >
                  <For each={filteredCommits()}>
                    {(commit) => {
                      const isSelected = () => currentCommitHash() === commit.hash;
                      const graphInfo = () => graphMap().get(commit.hash);
                      const isItemWorkdir = commit.hash === "WORKDIR";
                      const colWidth = 14;
                      const rowHeight = 44;

                      return (
                        <div
                          onClick={() => project() && selectCommit(project()!, commit.hash)}
                          class={`flex items-center gap-2 px-3 py-2 cursor-pointer transition-colors text-xs select-none ${
                            isSelected()
                              ? "bg-primary/10 border-l-2 border-primary"
                              : isItemWorkdir
                              ? "bg-amber-500/5 hover:bg-amber-500/10"
                              : "hover:bg-muted/40"
                          }`}
                        >
                          {/* DAG Graph Column */}
                          <div
                            class="shrink-0 relative self-stretch flex items-center justify-center"
                            style={{ width: `${maxColumns() * colWidth}px` }}
                          >
                            <svg class="absolute inset-0 w-full h-full pointer-events-none">
                              {graphInfo()?.connections.map((conn) => {
                                const x1 = conn.fromColumn * colWidth + colWidth / 2;
                                const y1 = rowHeight / 2;
                                const x2 = conn.toColumn * colWidth + colWidth / 2;
                                const y2 = rowHeight;
                                const strokeColor = isItemWorkdir
                                  ? "#f59e0b"
                                  : GRAPH_COLORS[conn.colorIndex % GRAPH_COLORS.length];

                                if (conn.fromColumn === conn.toColumn) {
                                  return (
                                    <line
                                      x1={x1}
                                      y1={0}
                                      x2={x2}
                                      y2={rowHeight}
                                      stroke={strokeColor}
                                      stroke-width="2"
                                      stroke-dasharray={isItemWorkdir ? "3 3" : undefined}
                                    />
                                  );
                                }
                                return (
                                  <path
                                    d={`M ${x1} ${y1} C ${x1} ${(y1 + y2) / 2}, ${x2} ${(y1 + y2) / 2}, ${x2} ${y2}`}
                                    fill="none"
                                    stroke={strokeColor}
                                    stroke-width="2"
                                    stroke-dasharray={isItemWorkdir ? "3 3" : undefined}
                                  />
                                );
                              })}
                              {(() => {
                                const info = graphInfo();
                                if (!info) return null;
                                const cx = info.column * colWidth + colWidth / 2;
                                const cy = rowHeight / 2;
                                const color = isItemWorkdir
                                  ? "#f59e0b"
                                  : GRAPH_COLORS[info.colorIndex % GRAPH_COLORS.length];
                                return (
                                  <circle
                                    cx={cx}
                                    cy={cy}
                                    r={isSelected() ? 4.5 : 3.5}
                                    fill={color}
                                    stroke="var(--background)"
                                    stroke-width="1.5"
                                  />
                                );
                              })()}
                            </svg>
                          </div>

                          {/* Commit Details */}
                          <div class="flex-1 min-w-0 flex flex-col gap-0.5">
                            <div class="flex items-center gap-1.5 min-w-0">
                              <Show when={isItemWorkdir}>
                                <Badge class="h-4 px-1 text-[10px] font-sans shrink-0 bg-amber-500/20 text-amber-600 dark:text-amber-400 border-amber-500/30">
                                  Uncommitted
                                </Badge>
                              </Show>
                              <Show when={commit.head && !isItemWorkdir}>
                                <Badge variant="secondary" class="h-4 px-1 text-[10px] font-sans shrink-0">
                                  HEAD
                                </Badge>
                              </Show>
                              <span class={`font-medium truncate leading-tight ${isItemWorkdir ? "text-amber-600 dark:text-amber-400 font-semibold" : "text-foreground"}`}>
                                {commit.subject}
                              </span>
                            </div>
                            <div class="flex items-center gap-2 text-[11px] text-muted-foreground min-w-0">
                              <span class="truncate">{commit.author}</span>
                              <span>·</span>
                              <span class="shrink-0">{formatRelativeTime(commit.time)}</span>
                              <Show when={!isItemWorkdir}>
                                <span class="ml-auto font-mono text-[10px] shrink-0 opacity-70">
                                  {commit.short}
                                </span>
                              </Show>
                            </div>
                          </div>
                        </div>
                      );
                    }}
                  </For>
                </Show>
              </Show>
            </Show>
          </div>
        </div>

        {/* Center Pane: Commit Header / Form & Diff Viewer */}
        <div class="flex-1 flex flex-col min-w-0 bg-background overflow-hidden">
          <Show
            when={currentCommitHash()}
            fallback={
              <div class="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground p-6">
                <GitCommitIcon class="size-8 stroke-[1.5]" />
                <p class="text-sm">Select a commit from the log to view details & diff.</p>
              </div>
            }
          >
            <Show
              when={!diffLoading()}
              fallback={
                <div class="flex h-full items-center justify-center gap-2 text-muted-foreground">
                  <Loader2 class="size-4 animate-spin" />
                  <span class="text-xs">Loading commit diff…</span>
                </div>
              }
            >
              <Show
                when={!diffError()}
                fallback={
                  <div class="p-6 text-center text-xs text-destructive">
                    Failed to load diff: {diffError()}
                  </div>
                }
              >
                {/* Commit Metadata / Commit Input Bar */}
                <div class="flex flex-col gap-2 p-4 border-b border-border bg-muted/5 shrink-0">
                  <Show
                    when={isWorkdir()}
                    fallback={
                      <>
                        <div class="flex items-start justify-between gap-4">
                          <h2 class="text-base font-semibold text-foreground leading-snug">
                            {selectedDiffResult()?.commit.subject}
                          </h2>
                          <CommitHashCopy hash={selectedDiffResult()?.commit.hash ?? ""} />
                        </div>
                        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                          <div class="flex items-center gap-1">
                            <span class="font-medium text-foreground">
                              {selectedDiffResult()?.commit.author}
                            </span>
                            <span class="opacity-70">&lt;{selectedDiffResult()?.commit.email}&gt;</span>
                          </div>
                          <span>·</span>
                          <span>{selectedDiffResult()?.commit.time ? formatAuthorTime(selectedDiffResult()!.commit.time) : ""}</span>
                          <Show when={selectedDiffResult()?.commit.parents && selectedDiffResult()!.commit.parents!.length > 0}>
                            <span>·</span>
                            <div class="flex items-center gap-1 font-mono text-[11px]">
                              <span>Parents:</span>
                              <For each={selectedDiffResult()!.commit.parents}>
                                {(pHash) => (
                                  <button
                                    type="button"
                                    onClick={() => project() && selectCommit(project()!, pHash)}
                                    class="text-primary hover:underline"
                                  >
                                    {pHash.substring(0, 7)}
                                  </button>
                                )}
                              </For>
                            </div>
                          </Show>
                        </div>
                      </>
                    }
                  >
                    {/* Commit Input Form for WORKDIR */}
                    <form onSubmit={handleCommitSubmit} class="flex flex-col gap-2.5">
                      <div class="flex items-center justify-between">
                        <div class="flex items-center gap-2">
                          <Badge class="bg-amber-500/20 text-amber-600 dark:text-amber-400 border-amber-500/30">
                            Uncommitted Changes
                          </Badge>
                          <span class="text-xs text-muted-foreground">
                            {stagedFiles().length} staged, {unstagedFiles().length + untrackedFiles().length} unstaged
                          </span>
                        </div>
                      </div>
                      <div class="flex gap-2">
                        <textarea
                          placeholder="Enter commit message..."
                          value={commitMessage()}
                          onInput={(e) => setCommitMessage(e.currentTarget.value)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                              handleCommitSubmit(e);
                            }
                          }}
                          rows={2}
                          class="flex-1 min-w-0 rounded-md border border-input bg-background p-2 text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                        />
                        <Button
                          type="submit"
                          size="sm"
                          disabled={!commitMessage().trim() || isCommitting()}
                          class="gap-1.5 h-auto px-4 self-end"
                        >
                          <Show when={isCommitting()} fallback={<Send class="size-3.5" />}>
                            <Loader2 class="size-3.5 animate-spin" />
                          </Show>
                          <span>
                            {isCommitting()
                              ? "Committing..."
                              : stagedFiles().length > 0
                              ? "Commit Staged Changes"
                              : "Commit All Changes"}
                          </span>
                        </Button>
                      </div>
                      <Show when={commitErr()}>
                        <p class="text-xs text-destructive font-medium">{commitErr()}</p>
                      </Show>
                    </form>
                  </Show>
                </div>

                {/* Diff Viewer Area */}
                <div class="flex-1 min-h-0 overflow-y-auto p-4 font-mono text-xs leading-relaxed">
                  <Show
                    when={selectedDiffResult()?.diff}
                    fallback={
                      <div class="text-muted-foreground text-center py-8">
                        No changes detected in working directory.
                      </div>
                    }
                  >
                    <DiffViewer
                      project={project()!}
                      hash={currentCommitHash()!}
                      diff={selectedDiffResult()!.diff}
                      selectedFile={currentFilePath()}
                    />
                  </Show>
                </div>
              </Show>
            </Show>
          </Show>
        </div>

        {/* Right Pane: Changed Files Sidebar (Sublime Merge Style) */}
        <Show when={currentCommitHash() && selectedDiffResult()}>
          <div class="w-64 min-w-[220px] max-w-[320px] shrink-0 flex flex-col bg-background border-l border-border">
            {/* Header */}
            <div class="flex h-8 shrink-0 items-center justify-between border-b border-border px-3 text-xs font-semibold text-muted-foreground bg-muted/10">
              <div class="flex items-center gap-1.5">
                <FileDiff class="size-3.5" />
                <span>Changed Files ({allFiles().length})</span>
              </div>
            </div>

            {/* Filter Files Input */}
            <div class="p-2 border-b border-border/60">
              <div class="relative">
                <Search class="absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
                <input
                  type="text"
                  placeholder="Filter files..."
                  value={fileSearchQuery()}
                  onInput={(e) => setFileSearchQuery(e.currentTarget.value)}
                  class="h-6 w-full rounded border border-input bg-background pl-7 pr-2 text-[11px] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
              </div>
            </div>

            {/* File Lists */}
            <div class="min-h-0 flex-1 overflow-y-auto divide-y divide-border/40">
              <div
                onClick={() => project() && selectDiffFile(project()!, null)}
                class={`flex items-center justify-between px-3 py-1.5 cursor-pointer text-xs select-none ${
                  currentFilePath() === null
                    ? "bg-primary/10 font-semibold text-primary"
                    : "hover:bg-muted/40 text-muted-foreground"
                }`}
              >
                <div class="flex items-center gap-1.5">
                  <FileCode class="size-3.5" />
                  <span>All files ({allFiles().length})</span>
                </div>
              </div>

              <Show
                when={isWorkdir()}
                fallback={
                  /* Regular Commit File List */
                  <For
                    each={allFiles().filter((f) =>
                      f.path.toLowerCase().includes(fileSearchQuery().toLowerCase().trim())
                    )}
                  >
                    {(file: GitFileChange) => {
                      const badge = getStatusBadge(file.status);
                      const isSelectedFile = () => currentFilePath() === file.path;

                      return (
                        <div
                          onClick={() => project() && selectDiffFile(project()!, file.path)}
                          class={`flex items-center justify-between gap-2 px-3 py-2 cursor-pointer transition-colors text-xs select-none ${
                            isSelectedFile()
                              ? "bg-primary/15 font-medium text-foreground border-l-2 border-primary"
                              : "hover:bg-muted/40"
                          }`}
                        >
                          <div class="flex items-center gap-2 min-w-0">
                            <span
                              class={`px-1 py-0.5 rounded text-[10px] font-mono border font-semibold shrink-0 ${badge.class}`}
                            >
                              {badge.label}
                            </span>
                            <span class="truncate font-mono text-[11px]" title={file.path}>
                              {file.path}
                            </span>
                          </div>
                          <div class="flex items-center gap-1 font-mono text-[10px] shrink-0">
                            <Show when={file.additions > 0}>
                              <span class="text-emerald-600 dark:text-emerald-400">+{file.additions}</span>
                            </Show>
                            <Show when={file.deletions > 0}>
                              <span class="text-rose-600 dark:text-rose-400">-{file.deletions}</span>
                            </Show>
                          </div>
                        </div>
                      );
                    }}
                  </For>
                }
              >
                {/* WORKDIR Categorized Staging Sections */}
                <div class="flex flex-col">
                  {/* Staged Changes Section */}
                  <Show when={stagedFiles().length > 0}>
                    <div class="flex items-center justify-between px-3 py-1.5 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 font-semibold text-[11px] border-b border-border/40">
                      <span>Staged Changes ({stagedFiles().length})</span>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (project()) stageGitFile(project()!, "", true, true);
                        }}
                        class="hover:underline text-[10px] font-mono flex items-center gap-0.5"
                        title="Unstage all staged files"
                      >
                        <Minus class="size-3" /> Unstage All
                      </button>
                    </div>
                    <For each={stagedFiles()}>
                      {(file: GitFileChange) => (
                        <FileRow
                          file={file}
                          selected={currentFilePath() === file.path}
                          onSelect={() => project() && selectDiffFile(project()!, file.path)}
                          onAction={() => project() && stageGitFile(project()!, file.path, true)}
                          actionLabel="Unstage"
                          actionIcon={<Minus class="size-3" />}
                        />
                      )}
                    </For>
                  </Show>

                  {/* Unstaged Changes Section */}
                  <Show when={unstagedFiles().length > 0}>
                    <div class="flex items-center justify-between px-3 py-1.5 bg-amber-500/10 text-amber-600 dark:text-amber-400 font-semibold text-[11px] border-b border-border/40">
                      <span>Unstaged Changes ({unstagedFiles().length})</span>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (project()) stageGitFile(project()!, "", false, true);
                        }}
                        class="hover:underline text-[10px] font-mono flex items-center gap-0.5"
                        title="Stage all modified files"
                      >
                        <Plus class="size-3" /> Stage All
                      </button>
                    </div>
                    <For each={unstagedFiles()}>
                      {(file: GitFileChange) => (
                        <FileRow
                          file={file}
                          selected={currentFilePath() === file.path}
                          onSelect={() => project() && selectDiffFile(project()!, file.path)}
                          onAction={() => project() && stageGitFile(project()!, file.path, false)}
                          actionLabel="Stage"
                          actionIcon={<Plus class="size-3" />}
                        />
                      )}
                    </For>
                  </Show>

                  {/* Untracked Files Section */}
                  <Show when={untrackedFiles().length > 0}>
                    <div class="flex items-center justify-between px-3 py-1.5 bg-muted/40 text-muted-foreground font-semibold text-[11px] border-b border-border/40">
                      <span>Untracked Files ({untrackedFiles().length})</span>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (project()) stageGitFile(project()!, "", false, true);
                        }}
                        class="hover:underline text-[10px] font-mono flex items-center gap-0.5"
                        title="Stage all untracked files"
                      >
                        <Plus class="size-3" /> Stage All
                      </button>
                    </div>
                    <For each={untrackedFiles()}>
                      {(file: GitFileChange) => (
                        <FileRow
                          file={file}
                          selected={currentFilePath() === file.path}
                          onSelect={() => project() && selectDiffFile(project()!, file.path)}
                          onAction={() => project() && stageGitFile(project()!, file.path, false)}
                          actionLabel="Stage"
                          actionIcon={<Plus class="size-3" />}
                        />
                      )}
                    </For>
                  </Show>
                </div>
              </Show>
            </div>
          </div>
        </Show>
      </div>
    </div>
  );
}

function FileRow(props: {
  file: GitFileChange;
  selected: boolean;
  onSelect: () => void;
  onAction: () => void;
  actionLabel: string;
  actionIcon: any;
}) {
  const badge = getStatusBadge(props.file.status);

  return (
    <div
      onClick={props.onSelect}
      class={`group flex items-center justify-between gap-2 px-3 py-2 cursor-pointer transition-colors text-xs select-none ${
        props.selected
          ? "bg-primary/15 font-medium text-foreground border-l-2 border-primary"
          : "hover:bg-muted/40"
      }`}
    >
      <div class="flex items-center gap-2 min-w-0 flex-1">
        <span class={`px-1 py-0.5 rounded text-[10px] font-mono border font-semibold shrink-0 ${badge.class}`}>
          {badge.label}
        </span>
        <span class="truncate font-mono text-[11px]" title={props.file.path}>
          {props.file.path}
        </span>
      </div>

      <div class="flex items-center gap-1 shrink-0">
        <Button
          variant="ghost"
          size="icon"
          class="size-5 opacity-80 group-hover:opacity-100 hover:bg-primary/20 text-foreground"
          onClick={(e) => {
            e.stopPropagation();
            props.onAction();
          }}
          title={`${props.actionLabel} ${props.file.path}`}
        >
          {props.actionIcon}
        </Button>
      </div>
    </div>
  );
}

function CommitHashCopy(props: { hash: string }) {
  const [copied, setCopied] = createSignal(false);
  const copy = () => {
    if (!props.hash) return;
    void navigator.clipboard.writeText(props.hash).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <Button
      variant="outline"
      size="sm"
      class="h-6 gap-1 font-mono text-[11px] px-2"
      onClick={copy}
    >
      <Show when={copied()} fallback={<Copy class="size-3" />}>
        <Check class="size-3 text-emerald-500" />
      </Show>
      <span>{props.hash.substring(0, 7)}</span>
    </Button>
  );
}

interface ParsedDiffLine {
  type: "header" | "add" | "delete" | "context" | "hunk";
  text: string;
  oldLine?: number;
  newLine?: number;
}

interface ParsedFileChunk {
  header: string;
  filePath: string;
  lines: ParsedDiffLine[];
}

function DiffViewer(props: {
  project: string;
  hash: string;
  diff: string;
  selectedFile: string | null;
}) {
  const fileChunks = createMemo(() => {
    const raw = props.diff;
    if (!raw) return [];
    const parts = raw.split(/^diff --git /m);
    const chunks: ParsedFileChunk[] = [];

    for (const part of parts) {
      if (!part.trim()) continue;
      const rawLines = part.split("\n");
      const headerLine = rawLines[0];
      let filePath = "";
      const match = headerLine.match(/b\/(.+)$/);
      if (match) {
        filePath = match[1];
      } else {
        filePath = headerLine;
      }

      const parsedLines: ParsedDiffLine[] = [];
      let oldLineNum = 1;
      let newLineNum = 1;

      for (let i = 1; i < rawLines.length; i++) {
        const line = rawLines[i];
        if (line.startsWith("@@")) {
          // Hunk header match @@ -oldStart,oldLen +newStart,newLen @@
          const hunkMatch = line.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
          if (hunkMatch) {
            oldLineNum = parseInt(hunkMatch[1], 10);
            newLineNum = parseInt(hunkMatch[2], 10);
          }
          parsedLines.push({ type: "hunk", text: line });
        } else if (line.startsWith("+") && !line.startsWith("+++")) {
          parsedLines.push({ type: "add", text: line, newLine: newLineNum++ });
        } else if (line.startsWith("-") && !line.startsWith("---")) {
          parsedLines.push({ type: "delete", text: line, oldLine: oldLineNum++ });
        } else if (line.startsWith(" ") || line === "") {
          parsedLines.push({ type: "context", text: line, oldLine: oldLineNum++, newLine: newLineNum++ });
        } else {
          parsedLines.push({ type: "header", text: line });
        }
      }

      chunks.push({
        header: `diff --git ${headerLine}`,
        filePath,
        lines: parsedLines,
      });
    }
    return chunks;
  });

  const visibleChunks = createMemo(() => {
    const filter = props.selectedFile;
    if (!filter) return fileChunks();
    return fileChunks().filter((c) => c.filePath === filter);
  });

  return (
    <div class="flex flex-col gap-6">
      <For each={visibleChunks()}>
        {(chunk) => (
          <div class="border border-border rounded-md overflow-hidden bg-card">
            {/* File Header Bar */}
            <div class="flex items-center justify-between px-3 py-1.5 bg-muted/40 border-b border-border text-xs font-semibold">
              <div class="flex items-center gap-2">
                <FileCode class="size-3.5 text-muted-foreground" />
                <span class="truncate">{chunk.filePath}</span>
              </div>
            </div>

            {/* Code Lines Table with Line Numbers Gutter */}
            <div class="overflow-x-auto">
              <table class="w-full border-collapse font-mono text-[11px] leading-relaxed">
                <tbody>
                  <For each={chunk.lines}>
                    {(line) => {
                      if (line.type === "hunk") {
                        return (
                          <tr class="bg-sky-500/10 text-sky-600 dark:text-sky-400 font-semibold select-none border-y border-sky-500/20">
                            <td class="w-10 text-right pr-2 py-1 text-[10px] opacity-40 border-r border-border/40 select-none">
                              ...
                            </td>
                            <td class="w-10 text-right pr-2 py-1 text-[10px] opacity-40 border-r border-border/40 select-none">
                              ...
                            </td>
                            <td class="px-3 py-1 font-mono text-[11px] flex items-center justify-between gap-2">
                              <span>{line.text}</span>
                              <button
                                type="button"
                                onClick={() => loadGitDiff(props.project, props.hash, 10000, true)}
                                class="flex items-center gap-1 text-[10px] hover:underline text-primary opacity-90 hover:opacity-100"
                                title="Expand full file context lines"
                              >
                                <Maximize2 class="size-3" /> Expand context
                              </button>
                            </td>
                          </tr>
                        );
                      }

                      if (line.type === "header") {
                        return (
                          <tr class="text-muted-foreground bg-muted/20">
                            <td class="w-10 border-r border-border/40"></td>
                            <td class="w-10 border-r border-border/40"></td>
                            <td class="px-3 py-0.5 whitespace-pre">{line.text}</td>
                          </tr>
                        );
                      }

                      let lineStyle = "text-foreground";
                      let bgStyle = "";

                      if (line.type === "add") {
                        lineStyle = "text-emerald-600 dark:text-emerald-400 font-medium";
                        bgStyle = "bg-emerald-500/10";
                      } else if (line.type === "delete") {
                        lineStyle = "text-rose-600 dark:text-rose-400 font-medium";
                        bgStyle = "bg-rose-500/10";
                      }

                      return (
                        <tr class={`hover:bg-muted/30 ${bgStyle}`}>
                          {/* Old Line Number */}
                          <td class="w-10 text-right pr-2 py-0.5 text-[10px] text-muted-foreground/60 select-none border-r border-border/40">
                            {line.oldLine ?? ""}
                          </td>
                          {/* New Line Number */}
                          <td class="w-10 text-right pr-2 py-0.5 text-[10px] text-muted-foreground/60 select-none border-r border-border/40">
                            {line.newLine ?? ""}
                          </td>
                          {/* Content */}
                          <td class={`px-3 py-0.5 whitespace-pre ${lineStyle}`}>
                            {line.text || " "}
                          </td>
                        </tr>
                      );
                    }}
                  </For>
                </tbody>
              </table>
            </div>
          </div>
        )}
      </For>
    </div>
  );
}
