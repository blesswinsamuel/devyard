import { createMemo, For, Show, type JSX } from "solid-js";
import { Archive, ArchiveRestore, ChevronRight, Cloud, GitBranch, Tag, Trash2 } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { confirm } from "~/app/ui-state";
import { createPersistedSignal } from "~/lib/persistence";
import { formatRelative, now } from "~/lib/format";
import { cn } from "~/lib/utils";
import { AheadBehind, PaneHeader } from "./badges";
import { stashEntry, stashPending, type GitBranchView, type GitLogView, type GitStashView } from "./git-data";

type SectionId = "branches" | "remotes" | "tags" | "stashes";

const [openState, setOpenState] = createPersistedSignal<Record<string, boolean>>("git.refs.open", {}, (raw) =>
  raw && typeof raw === "object" ? (raw as Record<string, boolean>) : undefined,
);
const isOpen = (id: string) => openState()[id] ?? true;
const toggle = (id: string) => setOpenState((s) => ({ ...s, [id]: !isOpen(id) }));

function Section(props: {
  id: SectionId;
  icon: JSX.Element;
  title: string;
  count: number;
  empty: string;
  children: JSX.Element;
}) {
  const open = () => isOpen(props.id);
  return (
    <section class="flex flex-col">
      <button
        type="button"
        class="focus-ring flex h-8 shrink-0 items-center gap-1.5 px-3 text-left text-2xs font-semibold text-muted-foreground hover:text-foreground"
        aria-expanded={open()}
        onClick={() => toggle(props.id)}
      >
        <ChevronRight class={cn("size-3 shrink-0 transition-transform", open() && "rotate-90")} />
        {props.icon}
        <span class="min-w-0 flex-1 truncate">{props.title}</span>
        <span class="tabular font-normal">{props.count}</span>
      </button>
      <Show when={open()}>
        <div class="flex flex-col pb-2">
          <Show
            when={props.count > 0}
            fallback={<p class="px-3 py-1 pl-8 text-2xs text-muted-foreground italic">{props.empty}</p>}
          >
            {props.children}
          </Show>
        </div>
      </Show>
    </section>
  );
}

function RefButton(props: { selected: boolean; title: string; onClick: () => void; class?: string; children: JSX.Element }) {
  return (
    <button
      type="button"
      class={cn(
        "focus-ring flex w-full min-w-0 items-center gap-1.5 py-1 pr-3 pl-8 text-left",
        props.selected ? "bg-accent text-accent-foreground" : "hover:bg-muted/60",
        props.class,
      )}
      title={props.title}
      aria-current={props.selected || undefined}
      onClick={props.onClick}
    >
      {props.children}
    </button>
  );
}

function BranchRow(props: { branch: GitBranchView; selected: boolean; onSelect: () => void }) {
  const b = () => props.branch;
  return (
    <RefButton
      selected={props.selected}
      title={`${b().name} (${b().hash.slice(0, 7)})${b().upstream ? ` → ${b().upstream}` : ""}`}
      onClick={props.onSelect}
    >
      <GitBranch class={cn("size-3 shrink-0", b().isActive ? "text-success" : "text-muted-foreground")} />
      <span class="flex min-w-0 flex-1 flex-col">
        <span class={cn("truncate font-mono text-xs", b().isActive && "font-semibold text-success")}>{b().name}</span>
        <Show when={b().upstream}>
          <span class="truncate font-mono text-2xs text-muted-foreground">→ {b().upstream}</span>
        </Show>
      </span>
      <AheadBehind ahead={b().ahead} behind={b().behind} upstream={b().upstream} />
      <Show when={b().isActive}>
        <span class="shrink-0 rounded-sm bg-success/15 px-1 text-2xs font-semibold text-success">HEAD</span>
      </Show>
    </RefButton>
  );
}

/** A stash entry: pick it to inspect its commit; pop or drop it from here. */
function StashRow(props: { project: string; stash: GitStashView; selected: boolean; onSelect: () => void }) {
  const s = () => props.stash;
  const pending = (op: "pop" | "drop") => stashPending(props.project, op, s().index);
  const run = (op: "pop" | "drop") => void stashEntry(props.project, op, s().index);
  const drop = async () => {
    if (
      await confirm({
        title: `Drop ${s().index}?`,
        description: "The stash is removed without restoring its changes. This cannot be undone.",
        confirmLabel: "Drop stash",
      })
    )
      run("drop");
  };
  return (
    <div
      role="button"
      tabindex="0"
      aria-current={props.selected || undefined}
      title={`${s().index}: ${s().name}`}
      class={cn(
        "focus-ring group flex w-full min-w-0 cursor-pointer items-center gap-1.5 py-1 pr-3 pl-8 text-left",
        props.selected ? "bg-accent text-accent-foreground" : "hover:bg-muted/60",
      )}
      onClick={props.onSelect}
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          props.onSelect();
        }
      }}
    >
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate text-xs">{s().name}</span>
        <span class="truncate font-mono text-2xs text-muted-foreground">
          {s().index} · {formatRelative(s().time, now())}
        </span>
      </span>
      <Button
        variant="ghost"
        size="icon-xs"
        class="text-muted-foreground lg:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100"
        aria-label={`Pop ${s().index}`}
        title={`Pop ${s().index} (restore its changes and remove the entry)`}
        disabled={pending("pop")}
        tabindex="-1"
        onClick={(e: MouseEvent) => {
          e.stopPropagation();
          run("pop");
        }}
      >
        <Show when={pending("pop")} fallback={<ArchiveRestore />}>
          <Spinner />
        </Show>
      </Button>
      <Button
        variant="ghost"
        size="icon-xs"
        class="text-muted-foreground lg:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100"
        aria-label={`Drop ${s().index}`}
        title={`Drop ${s().index} (remove the entry without restoring)`}
        disabled={pending("drop")}
        tabindex="-1"
        onClick={(e: MouseEvent) => {
          e.stopPropagation();
          void drop();
        }}
      >
        <Show when={pending("drop")} fallback={<Trash2 />}>
          <Spinner />
        </Show>
      </Button>
    </div>
  );
}

/** Local branches, remotes (grouped by remote), tags and stashes; picking one selects its commit. */
export function RefsPanel(props: {
  project: string;
  log: GitLogView;
  selected: string | undefined;
  onSelect: (hash: string) => void;
  header?: boolean;
}) {
  const local = createMemo(() => props.log.branches.filter((b) => !b.isRemote));
  const remotes = createMemo(() => {
    const groups = new Map<string, { name: string; short: string; hash: string }[]>();
    for (const b of props.log.branches) {
      if (!b.isRemote) continue;
      const slash = b.name.indexOf("/");
      const remote = slash > 0 ? b.name.slice(0, slash) : b.name;
      const list = groups.get(remote) ?? [];
      list.push({ name: b.name, short: slash > 0 ? b.name.slice(slash + 1) : b.name, hash: b.hash });
      groups.set(remote, list);
    }
    return [...groups].sort(([a], [b]) => a.localeCompare(b));
  });
  const remoteCount = () => props.log.branches.filter((b) => b.isRemote).length;
  // Annotated tags list the tag object's hash; the commit refs name the commit.
  const tagCommit = createMemo(() => {
    const map = new Map<string, string>();
    for (const c of props.log.commits) for (const r of c.refs) if (r.type === "tag") map.set(r.name, c.hash);
    return map;
  });

  return (
    <div class="flex h-full min-h-0 flex-col">
      <Show when={props.header !== false}>
        <PaneHeader icon={GitBranch} title="Refs" />
      </Show>
      <div class="min-h-0 flex-1 overflow-y-auto py-1">
        <Section
          id="branches"
          icon={<GitBranch class="size-3.5 text-success" />}
          title="Branches"
          count={local().length}
          empty="No branches"
        >
          <For each={local()}>
            {(b) => <BranchRow branch={b} selected={props.selected === b.hash} onSelect={() => props.onSelect(b.hash)} />}
          </For>
        </Section>
        <Section id="remotes" icon={<Cloud class="size-3.5" />} title="Remotes" count={remoteCount()} empty="No remotes">
          <For each={remotes()}>
            {([remote, list]) => {
              const id = `remote:${remote}`;
              return (
                <>
                  <button
                    type="button"
                    class="focus-ring flex h-7 items-center gap-1.5 pr-3 pl-6 text-left text-xs text-muted-foreground hover:text-foreground"
                    aria-expanded={isOpen(id)}
                    onClick={() => toggle(id)}
                  >
                    <ChevronRight class={cn("size-3 shrink-0 transition-transform", isOpen(id) && "rotate-90")} />
                    <span class="min-w-0 flex-1 truncate font-mono">{remote}</span>
                    <span class="tabular text-2xs">{list.length}</span>
                  </button>
                  <Show when={isOpen(id)}>
                    <For each={list}>
                      {(b) => (
                        <RefButton
                          class="pl-11"
                          selected={props.selected === b.hash}
                          title={`${b.name} (${b.hash.slice(0, 7)})`}
                          onClick={() => props.onSelect(b.hash)}
                        >
                          <span class="truncate font-mono text-xs">{b.short}</span>
                        </RefButton>
                      )}
                    </For>
                  </Show>
                </>
              );
            }}
          </For>
        </Section>
        <Section
          id="tags"
          icon={<Tag class="size-3.5 text-warning" />}
          title="Tags"
          count={props.log.tags.length}
          empty="No tags"
        >
          <For each={props.log.tags}>
            {(t) => {
              const hash = () => tagCommit().get(t.name) ?? t.hash;
              return (
                <RefButton
                  selected={props.selected === hash()}
                  title={`${t.name} (${hash().slice(0, 7)})`}
                  onClick={() => props.onSelect(hash())}
                >
                  <span class="truncate font-mono text-xs">{t.name}</span>
                </RefButton>
              );
            }}
          </For>
        </Section>
        <Section
          id="stashes"
          icon={<Archive class="size-3.5" />}
          title="Stashes"
          count={props.log.stashes.length}
          empty="No stashes"
        >
          <For each={props.log.stashes}>
            {(s) => (
              <StashRow
                project={props.project}
                stash={s}
                selected={props.selected === s.hash}
                onSelect={() => props.onSelect(s.hash)}
              />
            )}
          </For>
        </Section>
      </div>
    </div>
  );
}
