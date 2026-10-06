import { createMemo, For, Show, type JSX } from "solid-js";
import { Copy, FileCode, Files, Minus, PanelRightClose, Plus, Search, Undo2, X } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "~/components/ui/input-group";
import { Spinner } from "~/components/ui/spinner";
import { copyText } from "~/components/copy-button";
import { confirm } from "~/app/ui-state";
import { getAction } from "~/data/actions";
import { actionBlocked, actionPending, runAction } from "~/app/runtime";
import { cn } from "~/lib/utils";
import { displayPath, FileStatus, LineStats, PaneHeader } from "./badges";
import { discardChanges, stagePath, stagePending, type GitFileView } from "./git-data";
import { GitContextMenu, type GitMenuItem } from "./menu";
import { listKey } from "./nav";

export interface FileGroups {
  staged: GitFileView[];
  /** Unstaged tracked changes, then untracked files. */
  changes: GitFileView[];
  /** Every file (commits have no staging groups). */
  all: GitFileView[];
}

export function groupFiles(files: GitFileView[], query: string): FileGroups {
  const q = query.trim().toLowerCase();
  const all = q ? files.filter((f) => f.path.toLowerCase().includes(q) || f.oldPath.toLowerCase().includes(q)) : files;
  return {
    all,
    staged: all.filter((f) => f.staged),
    changes: [...all.filter((f) => f.unstaged && !f.untracked), ...all.filter((f) => f.untracked)],
  };
}

/** Row order for keyboard navigation: "All files" (null) first, then unique paths as shown. */
export function fileOrder(groups: FileGroups, workdir: boolean): (string | null)[] {
  const paths = workdir ? [...groups.staged, ...groups.changes].map((f) => f.path) : groups.all.map((f) => f.path);
  return [null, ...new Set(paths)];
}

const optionId = (path: string | null) => `git-file-${path === null ? "*" : encodeURIComponent(path)}`;

function FileRow(props: {
  file: GitFileView;
  selected: boolean;
  /** Unique within the list (a partially staged file shows in both groups). */
  id?: string;
  onSelect: () => void;
  stage?: { unstage: boolean; project: string };
}) {
  const pending = () => !!props.stage && stagePending(props.stage.project, props.file.path, props.stage.unstage);
  const discard = async () => {
    const stage = props.stage;
    if (!stage) return;
    const f = props.file;
    if (
      await confirm({
        title: f.untracked ? `Delete ${f.path}?` : `Discard changes in ${f.path}?`,
        description: f.untracked
          ? "The untracked file is deleted. This cannot be undone."
          : "Staged and unstaged changes are discarded and the file goes back to HEAD. This cannot be undone.",
        confirmLabel: f.untracked ? "Delete file" : "Discard changes",
      })
    )
      void discardChanges(stage.project, f.path);
  };
  const items = (): GitMenuItem[] => {
    const stage = props.stage;
    if (!stage) return [{ label: "Copy path", icon: Copy, onSelect: () => void copyText(props.file.path) }];
    return [
      {
        label: stage.unstage ? "Unstage" : "Stage",
        icon: stage.unstage ? Minus : Plus,
        onSelect: () => void stagePath(stage.project, props.file.path, stage.unstage),
        disabled: pending(),
      },
      { label: "Discard changes", icon: Undo2, destructive: true, onSelect: () => void discard(), disabled: pending() },
      { label: "Copy path", icon: Copy, separated: true, onSelect: () => void copyText(props.file.path) },
    ];
  };
  return (
    <GitContextMenu items={items()}>
      <div
        id={props.id}
        role="option"
        aria-selected={props.selected}
        class={cn(
          "group flex h-8 cursor-pointer items-center gap-2 px-3 select-none",
          props.selected ? "bg-accent text-accent-foreground" : "hover:bg-muted/60",
        )}
        title={displayPath(props.file)}
        onClick={props.onSelect}
      >
        <FileStatus file={props.file} />
        <span class="min-w-0 flex-1 truncate text-left font-mono text-xs" dir="rtl">
          <bdi>{displayPath(props.file)}</bdi>
        </span>
        <LineStats additions={props.file.additions} deletions={props.file.deletions} />
        <Show when={props.stage}>
          {(stage) => (
            <Button
              variant="ghost"
              size="icon-xs"
              class="text-muted-foreground lg:opacity-0 lg:group-hover:opacity-100 lg:focus-visible:opacity-100"
              aria-label={`${stage().unstage ? "Unstage" : "Stage"} ${props.file.path}`}
              title={stage().unstage ? "Unstage" : "Stage"}
              disabled={pending()}
              tabindex="-1"
              onClick={(e: MouseEvent) => {
                e.stopPropagation();
                void stagePath(stage().project, props.file.path, stage().unstage);
              }}
            >
              <Show when={pending()} fallback={stage().unstage ? <Minus /> : <Plus />}>
                <Spinner />
              </Show>
            </Button>
          )}
        </Show>
      </div>
    </GitContextMenu>
  );
}

function GroupHeader(props: { title: string; count: number; tone: "success" | "warning"; children: JSX.Element }) {
  return (
    <div
      class={cn(
        "flex h-8 items-center gap-2 border-y px-3 text-2xs font-semibold",
        props.tone === "success"
          ? "border-success/20 bg-success/10 text-success"
          : "border-warning/20 bg-warning/10 text-warning",
      )}
    >
      <span>{props.title}</span>
      <span class="tabular font-normal">{props.count}</span>
      <span class="ml-auto">{props.children}</span>
    </div>
  );
}

/** Stage-all / unstage-all / discard-all through the action registry (shared with ⌘K). */
function BulkButton(props: { id: "git.stage-all" | "git.unstage-all" | "git.discard-all"; project: string }) {
  const action = getAction(props.id);
  const target = () => ({ kind: "project" as const, project: props.project });
  const pending = () => actionPending(action, target());
  const Icon = () => (props.id === "git.stage-all" ? <Plus /> : props.id === "git.unstage-all" ? <Minus /> : <Undo2 />);
  return (
    <Button
      variant="ghost"
      size="xs"
      class={cn(
        "h-6 hover:bg-background/60",
        props.id === "git.discard-all" ? "text-destructive hover:text-destructive" : "text-current",
      )}
      disabled={pending() || actionBlocked(action)}
      title={action.label}
      onClick={(e: MouseEvent) => {
        e.stopPropagation();
        void runAction(action, target());
      }}
    >
      <Show when={pending()} fallback={<Icon />}>
        <Spinner />
      </Show>
      {props.id === "git.discard-all" ? "Discard" : action.label}
    </Button>
  );
}

export function FileList(props: {
  project: string;
  files: GitFileView[];
  workdir: boolean;
  selected: string | null;
  query: string;
  onQuery: (q: string) => void;
  onSelect: (path: string | null, how: "click" | "key") => void;
  onOpen: () => void;
  onHide?: () => void;
  header?: boolean;
}) {
  const groups = createMemo(() => groupFiles(props.files, props.query));
  const order = createMemo(() => fileOrder(groups(), props.workdir));
  const totals = createMemo(() =>
    props.files.reduce((acc, f) => ({ add: acc.add + f.additions, del: acc.del + f.deletions }), { add: 0, del: 0 }),
  );
  let listEl!: HTMLDivElement;

  const onKey = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === " " && props.workdir) {
      // Space stages the selected file (or unstages it when fully staged).
      e.preventDefault();
      const file = props.files.find((f) => f.path === props.selected);
      if (file) void stagePath(props.project, file.path, file.staged && !file.unstaged && !file.untracked);
      return;
    }
    const list = order();
    const res = listKey(e.key, list.indexOf(props.selected), list.length);
    if (!res) return;
    e.preventDefault();
    e.stopPropagation();
    if ("open" in res) props.onOpen();
    else {
      const path = list[res.index]!;
      props.onSelect(path, "key");
      queueMicrotask(() => listEl.querySelector(`#${CSS.escape(optionId(path))}`)?.scrollIntoView({ block: "nearest" }));
    }
  };

  const row = (file: GitFileView, stage?: { unstage: boolean }, first = true) => (
    <FileRow
      file={file}
      id={first ? optionId(file.path) : undefined}
      selected={props.selected === file.path}
      onSelect={() => props.onSelect(file.path, "click")}
      stage={stage ? { ...stage, project: props.project } : undefined}
    />
  );

  return (
    <div class="flex h-full min-h-0 flex-col">
      <Show when={props.header !== false}>
        <PaneHeader icon={Files} title="Changed files" count={props.files.length}>
          <Show when={props.onHide}>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Hide changed files"
              title="Hide changed files"
              onClick={() => props.onHide?.()}
            >
              <PanelRightClose />
            </Button>
          </Show>
        </PaneHeader>
      </Show>
      <div class="shrink-0 border-b p-2">
        <InputGroup class="h-7">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            placeholder="Filter files"
            aria-label="Filter files"
            value={props.query}
            onInput={(e) => props.onQuery(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") props.onQuery("");
              else if (e.key === "ArrowDown") {
                e.preventDefault();
                listEl.focus();
              }
            }}
          />
          <Show when={props.query}>
            <InputGroupAddon align="inline-end">
              <InputGroupButton size="icon-xs" aria-label="Clear filter" onClick={() => props.onQuery("")}>
                <X />
              </InputGroupButton>
            </InputGroupAddon>
          </Show>
        </InputGroup>
      </div>
      <div
        ref={listEl}
        class="focus-ring min-h-0 flex-1 overflow-y-auto"
        tabindex="0"
        role="listbox"
        aria-label="Changed files"
        data-git-list
        aria-activedescendant={optionId(props.selected)}
        data-git-files
        onKeyDown={onKey}
      >
        <div
          id={optionId(null)}
          role="option"
          aria-selected={props.selected === null}
          class={cn(
            "flex h-8 cursor-pointer items-center gap-2 px-3 select-none",
            props.selected === null ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground hover:bg-muted/60",
          )}
          onClick={() => props.onSelect(null, "click")}
        >
          <FileCode class="size-3.5 shrink-0" />
          <span class="flex-1">All files</span>
          <LineStats additions={totals().add} deletions={totals().del} />
        </div>
        <Show when={props.workdir} fallback={<For each={groups().all}>{(f) => row(f)}</For>}>
          <Show when={groups().staged.length}>
            <GroupHeader title="Staged" count={groups().staged.length} tone="success">
              <BulkButton id="git.unstage-all" project={props.project} />
            </GroupHeader>
            <For each={groups().staged}>{(f) => row(f, { unstage: true })}</For>
          </Show>
          <Show when={groups().changes.length}>
            <GroupHeader title="Changes" count={groups().changes.length} tone="warning">
              <BulkButton id="git.stage-all" project={props.project} />
              <BulkButton id="git.discard-all" project={props.project} />
            </GroupHeader>
            <For each={groups().changes}>{(f) => row(f, { unstage: false }, !f.staged)}</For>
          </Show>
        </Show>
        <Show when={props.query && !groups().all.length}>
          <p class="px-3 py-4 text-center text-2xs text-muted-foreground">No files match “{props.query}”.</p>
        </Show>
      </div>
    </div>
  );
}
