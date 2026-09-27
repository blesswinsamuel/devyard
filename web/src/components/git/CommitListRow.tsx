import { For, Show } from "solid-js";
import { Archive, Check, GitBranch, Tag } from "lucide-solid";
import type { GitCommit } from "~/lib/types";
import { formatRelativeTime } from "~/lib/format";
import { Badge } from "~/components/badge";
import { cn } from "~/lib/utils";
import type { CommitGraphInfo } from "~/lib/git_graph";
import { CommitGraphCell } from "./CommitGraphCell";

/** Badge for a git ref (branch / remote / tag / stash / HEAD). */
export function RefBadge(props: {
  ref: { name: string; type: string; isActive?: boolean };
  hasActiveBranch: boolean;
}) {
  const r = () => props.ref;
  if (r().type === "branch" && r().isActive) {
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
    return (
      <Badge variant="secondary" class="font-mono text-[10px]">
        HEAD
      </Badge>
    );
  }
  return null;
}

/**
 * One row in the commit list: DAG strip on the left and the commit metadata
 * (subject, refs, author, relative time, stat summary) on the right.
 */
export function CommitListRow(props: {
  commit: GitCommit;
  info: CommitGraphInfo | undefined;
  columns: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const isWorkdir = () => props.commit.hash === "WORKDIR";

  return (
    <button
      type="button"
      data-commit={props.commit.hash}
      onClick={props.onSelect}
      class={cn(
        "relative flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors",
        props.selected
          ? "bg-accent"
          : isWorkdir()
            ? "hover:bg-warning/8"
            : "hover:bg-muted/50"
      )}
    >
      {/* selection bar */}
      <span
        class={cn(
          "absolute left-0 top-0 h-full w-[2.5px]",
          props.selected ? "bg-primary" : "bg-transparent"
        )}
      />
      <CommitGraphCell
        info={props.info}
        columns={props.columns}
        selected={props.selected}
        workdir={isWorkdir()}
      />

      <div class="flex min-w-0 flex-1 flex-col gap-0.5">
        <div class="flex min-w-0 flex-wrap items-center gap-1">
          <Show when={isWorkdir()}>
            <Badge class="border-warning/40 bg-warning/15 text-[10px] text-warning">Uncommitted</Badge>
          </Show>
          <Show when={props.commit.refs && props.commit.refs.length > 0}>
            <For each={props.commit.refs}>
              {(ref) => (
                <RefBadge
                  ref={ref}
                  hasActiveBranch={!!props.commit.refs?.some((r) => r.type === "branch" && r.isActive)}
                />
              )}
            </For>
          </Show>
          <span class={cn("truncate leading-tight", isWorkdir() ? "font-semibold text-warning" : "")}>
            {props.commit.subject}
          </span>
        </div>
        <div class="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <span class="truncate">{props.commit.author}</span>
          <span class="shrink-0 opacity-50">·</span>
          <span class="shrink-0">{formatRelativeTime(props.commit.time)}</span>
          <div class="ml-auto flex shrink-0 items-center gap-1.5 font-mono text-[10px] tabular">
            <Show when={props.commit.additions > 0 || props.commit.deletions > 0}>
              <span class="flex items-center gap-1">
                <Show when={props.commit.additions > 0}>
                  <span class="text-success">+{props.commit.additions}</span>
                </Show>
                <Show when={props.commit.deletions > 0}>
                  <span class="text-destructive">−{props.commit.deletions}</span>
                </Show>
              </span>
            </Show>
            <Show when={!isWorkdir()}>
              <span class="opacity-60">{props.commit.short}</span>
            </Show>
          </div>
        </div>
      </div>
    </button>
  );
}
