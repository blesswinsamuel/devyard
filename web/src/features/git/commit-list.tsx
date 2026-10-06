import { createEffect, For, on, Show, type JSX } from "solid-js";
import { createVirtualizer } from "@tanstack/solid-virtual";
import { Copy, GitBranchPlus, GitCommitHorizontal, Search, X } from "lucide-solid";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "~/components/ui/input-group";
import { copyText } from "~/components/copy-button";
import { promptGitBranch } from "~/app/ui-state";
import { formatRelative, now } from "~/lib/format";
import { cn } from "~/lib/utils";
import { PaneHeader, LineStats, RefBadge } from "./badges";
import { WORKDIR, type GitCommitView } from "./git-data";
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

function CommitRow(props: {
  commit: GitCommitView;
  project: string;
  info: CommitGraphInfo | undefined;
  columns: number;
  showGraph: boolean;
  selected: boolean;
  onClick: () => void;
}) {
  const workdir = () => props.commit.hash === WORKDIR;
  const detached = () => !props.commit.refs.some((r) => r.type === "branch" && r.isActive);
  const menu = (): GitMenuItem[] => [
    { label: "Copy hash", icon: Copy, onSelect: () => void copyText(props.commit.hash) },
    {
      label: `New branch at ${props.commit.short}…`,
      icon: GitBranchPlus,
      separated: true,
      onSelect: () => void promptGitBranch(props.project, props.commit.hash),
    },
  ];
  const row = () => (
    <div
      id={rowId(props.commit.hash)}
      role="option"
      aria-selected={props.selected}
      class={cn(
        "relative flex h-full cursor-pointer items-stretch gap-2 pr-3 pl-1 select-none",
        props.selected ? "bg-accent" : "hover:bg-muted/60",
      )}
      onClick={props.onClick}
    >
      <span class={cn("absolute inset-y-0 left-0 w-0.5", props.selected && "bg-primary")} aria-hidden="true" />
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
                          info={props.graph.get(commit().hash)}
                          columns={props.columns}
                          showGraph={!filtering()}
                          selected={commit().hash === props.selected}
                          onClick={() => props.onSelect(commit().hash, "click")}
                        />
                      </div>
                    )}
                  </Show>
                )}
              </For>
            </div>
          </Show>
        </div>
      </div>
    </div>
  );
}
