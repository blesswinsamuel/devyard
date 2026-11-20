import { createEffect, For, on, Show, type JSX } from "solid-js";
import { createVirtualizer } from "@tanstack/solid-virtual";
import {
  Cherry,
  ChevronsDown,
  Copy,
  FileText,
  GitBranch,
  GitBranchPlus,
  GitCommitHorizontal,
  GitCompare,
  GitMerge,
  GitPullRequestArrow,
  RotateCcw,
  Search,
  Tag,
  Undo2,
  X,
} from "lucide-solid";
import { Button } from "~/components/ui/button";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "~/components/ui/input-group";
import { Spinner } from "~/components/ui/spinner";
import { copyText } from "~/components/copy-button";
import { confirm, promptGitBranch, promptGitTag } from "~/app/ui-state";
import { formatRelative, now } from "~/lib/format";
import { cn } from "~/lib/utils";
import { PaneHeader, LineStats, RefBadge } from "./badges";
import {
  WORKDIR,
  checkoutBranch,
  checkoutCommit,
  cherryPickCommit,
  mergeCommit,
  rebaseOnto,
  resetToCommit,
  revertCommit,
  type GitCommitView,
} from "./git-data";
import type { CommitGraphInfo } from "./graph";
import { GraphCell } from "./graph-cell";
import { GitContextMenu, type GitMenuItem } from "./menu";
import { listKey } from "./nav";

/** Fixed row height (px): rows are two lines, so the graph segments line up. */
const ROW_H = 48;

/** Case-insensitive match on subject, author or hash. */
export function filterCommits(commits: GitCommitView[], query: string): GitCommitView[] {
  const q = query.trim().toLowerCase();
  if (!q) return commits;
  return commits.filter(
    (c) => c.subject.toLowerCase().includes(q) || c.author.toLowerCase().includes(q) || c.hash.toLowerCase().startsWith(q),
  );
}

const rowId = (hash: string) => `git-commit-${hash}`;

/**
 * The highlight variant of a commit row: the selected commit wins, and the
 * pinned comparison base is tinted below it so both sides of a ⌘/Ctrl-click
 * comparison stay visible in the list.
 */
export function commitRowClass(selected: boolean, base: boolean): string {
  return selected ? "bg-accent" : base ? "bg-primary/10" : "hover:bg-muted/60";
}

function CommitRow(props: {
  commit: GitCommitView;
  project: string;
  activeBranch?: string;
  info: CommitGraphInfo | undefined;
  columns: number;
  showGraph: boolean;
  selected: boolean;
  base: string | undefined;
  onClick: () => void;
  /** ⌘/Ctrl-click pins or completes a commit comparison. */
  onCompareClick?: (hash: string) => void;
  onCompare: (hash: string) => void;
  onClearCompare: () => void;
}) {
  const workdir = () => props.commit.hash === WORKDIR;
  const detached = () => !props.commit.refs.some((r) => r.type === "branch" && r.isActive);
  const isBase = () => props.base === props.commit.hash;
  const isDetached = () => !props.activeBranch || props.activeBranch === "(detached)";
  const targetLabel = () => (isDetached() ? "HEAD" : props.activeBranch!);
  const hardReset = async () => {
    const ok = await confirm({
      title: `Hard reset ${targetLabel()} to ${props.commit.short}?`,
      description: `Moves ${targetLabel()} to ${props.commit.short} and discards all staged and unstaged changes. This cannot be undone.`,
      confirmLabel: "Hard reset",
    });
    if (ok) void resetToCommit(props.project, props.commit.hash, props.commit.short, "hard");
  };
  const menu = (): GitMenuItem[] => {
    const c = props.commit;
    const target = targetLabel();
    const localBranches = c.refs.filter((r) => r.type === "branch" && !r.isActive);
    return [
      ...localBranches.map((b) => ({
        label: `Checkout ${b.name}`,
        icon: GitBranch,
        onSelect: () => void checkoutBranch(props.project, b.name),
      })),
      {
        label: `Checkout ${c.short} (detached)`,
        icon: GitBranch,
        disabled: c.head && isDetached(),
        onSelect: () => void checkoutCommit(props.project, c.hash, c.short),
      },
      {
        label: `Cherry-pick ${c.short}`,
        icon: Cherry,
        separated: true,
        disabled: c.head,
        onSelect: () => void cherryPickCommit(props.project, c.hash, c.short),
      },
      {
        label: `Revert ${c.short}`,
        icon: Undo2,
        onSelect: () => void revertCommit(props.project, c.hash, c.short),
      },
      {
        label: `Merge ${c.short} into ${target}`,
        icon: GitMerge,
        disabled: c.head,
        onSelect: () => void mergeCommit(props.project, c.hash, c.short),
      },
      {
        label: `Rebase ${target} onto ${c.short}`,
        icon: GitPullRequestArrow,
        disabled: c.head,
        onSelect: () => void rebaseOnto(props.project, c.hash, c.short),
      },
      {
        label: `Reset ${target} to ${c.short}`,
        icon: RotateCcw,
        children: [
          {
            label: "Soft (keep staged)",
            onSelect: () => void resetToCommit(props.project, c.hash, c.short, "soft"),
          },
          {
            label: "Mixed (keep working tree)",
            onSelect: () => void resetToCommit(props.project, c.hash, c.short, "mixed"),
          },
          {
            label: "Hard (discard all changes)…",
            destructive: true,
            separated: true,
            onSelect: () => void hardReset(),
          },
        ],
      },
      {
        label: `New branch at ${c.short}…`,
        icon: GitBranchPlus,
        separated: true,
        onSelect: () => void promptGitBranch(props.project, c.hash),
      },
      { label: `Tag ${c.short}…`, icon: Tag, onSelect: () => void promptGitTag(props.project, c.hash) },
      {
        label: isBase() ? "Clear comparison" : "Compare from here",
        icon: GitCompare,
        separated: true,
        onSelect: () => (isBase() ? props.onClearCompare() : props.onCompare(c.hash)),
      },
      { label: "Copy hash", icon: Copy, separated: true, onSelect: () => void copyText(c.hash) },
      { label: "Copy commit message", icon: FileText, onSelect: () => void copyText(c.subject) },
    ];
  };
  const row = () => (
    <div
      id={rowId(props.commit.hash)}
      role="option"
      aria-selected={props.selected}
      title={isBase() ? "Comparison base — ⌘/Ctrl-click to clear" : undefined}
      class={cn(
        "relative flex h-full cursor-pointer items-stretch gap-2 pr-3 pl-1 select-none",
        commitRowClass(props.selected, isBase()),
      )}
      onClick={(e) => {
        // Sublime-Merge-style ⌘/Ctrl-click pins or completes a comparison.
        if (e.metaKey || e.ctrlKey) {
          e.preventDefault();
          props.onCompareClick?.(props.commit.hash);
        } else {
          props.onClick();
        }
      }}
    >
      <span class={cn("absolute inset-y-0 left-0 w-0.5", props.selected && "bg-primary")} aria-hidden="true" />
      <Show when={isBase()}>
        <span class="absolute inset-y-0 right-0 w-0.5 bg-primary/60" aria-hidden="true" />
      </Show>
      <Show when={props.showGraph} fallback={<span class="w-1" />}>
        <GraphCell info={props.info} columns={props.columns} selected={props.selected} workdir={workdir()} />
      </Show>
      <div class="flex min-w-0 flex-1 flex-col justify-center gap-0.5">
        <div class="flex min-w-0 items-center gap-1">
          <Show when={props.commit.refs.length}>
            <span class="flex max-w-3/5 min-w-0 shrink-0 items-center gap-1 overflow-hidden">
              <For each={props.commit.refs}>{(ref) => <RefBadge value={ref} detached={detached()} />}</For>
            </span>
          </Show>
          <span
            class={cn("min-w-0 truncate", workdir() ? "font-medium text-warning" : props.selected && "text-accent-foreground")}
          >
            {workdir() ? "Uncommitted changes" : props.commit.subject}
          </span>
        </div>
        <div class="flex min-w-0 items-center gap-1.5 text-2xs text-muted-foreground">
          <span class="min-w-0 truncate">
            {workdir()
              ? `${props.commit.filesChanged} ${props.commit.filesChanged === 1 ? "file" : "files"} changed`
              : props.commit.author}
          </span>
          <Show when={!workdir()}>
            <span class="shrink-0" aria-hidden="true">
              ·
            </span>
            <span class="shrink-0" title={new Date(props.commit.time).toLocaleString()}>
              {formatRelative(props.commit.time, now())}
            </span>
          </Show>
          <span class="ml-auto flex shrink-0 items-center gap-1.5">
            <LineStats additions={props.commit.additions} deletions={props.commit.deletions} />
            <Show when={!workdir()}>
              <span class="font-mono">{props.commit.short}</span>
            </Show>
          </span>
        </div>
      </div>
    </div>
  );
  return (
    <Show when={!workdir()} fallback={row()}>
      <GitContextMenu class="h-full" items={menu()}>
        {row()}
      </GitContextMenu>
    </Show>
  );
}

export function CommitList(props: {
  project: string;
  activeBranch?: string;
  commits: GitCommitView[];
  /** Commits before filtering (for the count). */
  total: number;
  graph: Map<string, CommitGraphInfo>;
  columns: number;
  selected: string | undefined;
  query: string;
  onQuery: (q: string) => void;
  onSelect: (hash: string, how: "click" | "key") => void;
  onOpen: () => void;
  searchRef?: (el: HTMLInputElement) => void;
  /** Paging: whether older commits exist beyond the loaded pages. */
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  /** The pinned comparison base (a commit hash), if any. */
  base?: string;
  onCompare?: (hash: string) => void;
  onClearCompare?: () => void;
  /** ⌘/Ctrl-click pins or completes a commit comparison. */
  onCompareClick?: (hash: string) => void;
  /** Extra content above the search box (e.g. the file-history chip). */
  banner?: JSX.Element;
  /** Pane title row (off inside mobile tabs, which already name the pane). */
  header?: boolean;
  status?: JSX.Element;
}) {
  let scrollEl!: HTMLDivElement;
  const selectedIndex = () => props.commits.findIndex((c) => c.hash === props.selected);
  const filtering = () => props.query.trim() !== "";

  const virtualizer = createVirtualizer({
    get count() {
      return props.commits.length;
    },
    getScrollElement: () => scrollEl,
    estimateSize: () => ROW_H,
    overscan: 12,
    getItemKey: (i: number) => props.commits[i]?.hash ?? i,
  });

  // Keep the selection visible: steps (j/k) scroll just enough, jumps
  // (deep links, refs, parents) center the commit.
  createEffect(
    on(
      () => [selectedIndex(), props.commits.length] as const,
      ([i], prev) => {
        if (i < 0) return;
        const step = prev !== undefined && prev[0] >= 0 && Math.abs(i - prev[0]) <= 1;
        queueMicrotask(() => virtualizer.scrollToIndex(i, { align: step ? "auto" : "center" }));
      },
    ),
  );

  const onKey = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const res = listKey(e.key, selectedIndex(), props.commits.length);
    if (!res) return;
    e.preventDefault();
    e.stopPropagation();
    if ("open" in res) props.onOpen();
    else props.onSelect(props.commits[res.index]!.hash, "key");
  };

  return (
    <div class="flex h-full min-h-0 flex-col">
      <Show when={props.header !== false}>
        <PaneHeader icon={GitCommitHorizontal} title="Commits" count={filtering() ? undefined : props.total}>
          <Show when={filtering()}>
            <span class="tabular text-2xs text-muted-foreground">
              {props.commits.length} of {props.total}
            </span>
          </Show>
        </PaneHeader>
      </Show>
      {props.banner}
      <div class="shrink-0 border-b p-2">
        <InputGroup class="h-7">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            ref={props.searchRef}
            placeholder="Search subject, author, hash"
            aria-label="Search commits"
            value={props.query}
            onInput={(e) => props.onQuery(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                props.onQuery("");
                scrollEl.focus();
              } else if (e.key === "ArrowDown" || e.key === "Enter") {
                e.preventDefault();
                scrollEl.focus();
                if (selectedIndex() < 0 && props.commits[0]) props.onSelect(props.commits[0].hash, "key");
              }
            }}
          />
          <Show when={props.query}>
            <InputGroupAddon align="inline-end">
              <InputGroupButton size="icon-xs" aria-label="Clear search" onClick={() => props.onQuery("")}>
                <X />
              </InputGroupButton>
            </InputGroupAddon>
          </Show>
        </InputGroup>
      </div>
      <div class="relative min-h-0 flex-1">
        <div
          ref={scrollEl}
          class="focus-ring absolute inset-0 overflow-y-auto"
          tabindex="0"
          role="listbox"
          aria-label="Commits"
          data-git-list
          aria-activedescendant={props.selected && selectedIndex() >= 0 ? rowId(props.selected) : undefined}
          onKeyDown={onKey}
        >
          <Show when={props.commits.length} fallback={props.status}>
            <div class="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
              <For each={virtualizer.getVirtualItems()}>
                {(item) => (
                  <Show when={props.commits[item.index]}>
                    {(commit) => (
                      <div
                        class="absolute inset-x-0 top-0"
                        style={{ height: `${ROW_H}px`, transform: `translateY(${item.start}px)` }}
                      >
                        <CommitRow
                          commit={commit()}
                          project={props.project}
                          activeBranch={props.activeBranch}
                          info={props.graph.get(commit().hash)}
                          columns={props.columns}
                          showGraph={!filtering()}
                          selected={commit().hash === props.selected}
                          base={props.base}
                          onClick={() => props.onSelect(commit().hash, "click")}
                          onCompareClick={(h) => props.onCompareClick?.(h)}
                          onCompare={(h) => props.onCompare?.(h)}
                          onClearCompare={() => props.onClearCompare?.()}
                        />
                      </div>
                    )}
                  </Show>
                )}
              </For>
            </div>
          </Show>
          <Show when={props.hasMore && !filtering()}>
            <div class="p-3">
              <Button
                variant="outline"
                size="sm"
                class="w-full"
                disabled={props.loadingMore}
                aria-busy={props.loadingMore || undefined}
                onClick={() => props.onLoadMore?.()}
              >
                <Show when={props.loadingMore} fallback={<ChevronsDown />}>
                  <Spinner />
                </Show>
                Load older commits
              </Button>
            </div>
          </Show>
        </div>
      </div>
    </div>
  );
}
