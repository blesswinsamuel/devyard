import { Show, type JSX } from "solid-js";
import { Archive, GitBranch, Tag } from "lucide-solid";
import { cn } from "~/lib/utils";
import type { GitFileView, GitRefView } from "./git-data";

const REF_BASE =
  "inline-flex h-4 max-w-40 shrink-0 items-center gap-0.5 rounded-sm border px-1 font-mono text-2xs leading-none [&>svg]:size-2.5 [&>svg]:shrink-0";

/** A ref label in the commit list (branch, remote, tag, stash, detached HEAD). */
// Not `ref`: Solid reserves that prop name for element refs.
export function RefBadge(props: { value: GitRefView; detached: boolean }) {
  const r = () => props.value;
  const label = (icon: JSX.Element | null, cls: string) => (
    <span class={cn(REF_BASE, cls)} title={`${r().type}: ${r().name}`}>
      {icon}
      <span class="truncate">{r().name}</span>
    </span>
  );
  switch (r().type) {
    case "branch":
      return r().isActive
        ? label(<GitBranch />, "border-success/30 bg-success/15 font-semibold text-success")
        : label(<GitBranch />, "border-primary/20 bg-primary/10 text-primary");
    case "remote":
      return label(null, "border-border-strong text-muted-foreground");
    case "tag":
      return label(<Tag />, "border-warning/30 bg-warning/15 text-warning");
    case "stash":
      return label(<Archive />, "border-border bg-muted text-muted-foreground");
    case "head":
      return props.detached ? label(null, "border-info/30 bg-info/15 text-info") : null;
    default:
      return null;
  }
}

/** ↑n / ↓n against the upstream; renders nothing for zero. */
export function AheadBehind(props: { ahead?: number; behind?: number; upstream?: string; class?: string }) {
  const up = () => props.upstream || "upstream";
  return (
    <>
      <Show when={(props.ahead ?? 0) > 0}>
        <span
          class={cn("tabular shrink-0 rounded-full bg-success/15 px-1.5 font-mono text-2xs text-success", props.class)}
          title={`${props.ahead} ahead of ${up()}`}
        >
          ↑{props.ahead}
        </span>
      </Show>
      <Show when={(props.behind ?? 0) > 0}>
        <span
          class={cn("tabular shrink-0 rounded-full bg-warning/15 px-1.5 font-mono text-2xs text-warning", props.class)}
          title={`${props.behind} behind ${up()}`}
        >
          ↓{props.behind}
        </span>
      </Show>
    </>
  );
}

/** +a −d line counts. */
export function LineStats(props: { additions: number; deletions: number; class?: string }) {
  return (
    <Show when={props.additions > 0 || props.deletions > 0}>
      <span class={cn("tabular inline-flex shrink-0 items-center gap-1 font-mono text-2xs", props.class)}>
        <Show when={props.additions > 0}>
          <span class="text-success">+{props.additions}</span>
        </Show>
        <Show when={props.deletions > 0}>
          <span class="text-destructive">−{props.deletions}</span>
        </Show>
      </span>
    </Show>
  );
}

const FILE_STATUS: Record<string, { label: string; title: string; class: string }> = {
  A: { label: "A", title: "added", class: "border-success/30 bg-success/15 text-success" },
  D: { label: "D", title: "deleted", class: "border-destructive/30 bg-destructive/15 text-destructive" },
  R: { label: "R", title: "renamed", class: "border-info/30 bg-info/15 text-info" },
  C: { label: "C", title: "copied", class: "border-info/30 bg-info/15 text-info" },
  U: { label: "U", title: "untracked", class: "border-border-strong bg-muted text-muted-foreground" },
  M: { label: "M", title: "modified", class: "border-warning/30 bg-warning/15 text-warning" },
};

export function FileStatus(props: { file: GitFileView }) {
  const s = () => FILE_STATUS[props.file.untracked ? "U" : (props.file.status || "M").toUpperCase()[0]!] ?? FILE_STATUS.M!;
  return (
    <span
      class={cn(
        "inline-flex size-4 shrink-0 items-center justify-center rounded-sm border font-mono text-2xs font-semibold leading-none",
        s().class,
      )}
      title={s().title}
    >
      {s().label}
    </span>
  );
}

export const displayPath = (f: GitFileView) => (f.oldPath && f.oldPath !== f.path ? `${f.oldPath} → ${f.path}` : f.path);

/** Title row of a pane: label, count and trailing controls. */
export function PaneHeader(props: {
  icon: (p: { class?: string }) => JSX.Element;
  title: string;
  count?: number;
  children?: JSX.Element;
}) {
  const Icon = props.icon;
  return (
    <div class="flex h-9 shrink-0 items-center gap-1.5 border-b px-3 text-2xs font-medium tracking-wide text-muted-foreground uppercase">
      <Icon class="size-3.5" />
      <span>{props.title}</span>
      <Show when={props.count !== undefined}>
        <span class="tabular font-normal">{props.count}</span>
      </Show>
      <div class="ml-auto flex items-center gap-1 normal-case">{props.children}</div>
    </div>
  );
}
