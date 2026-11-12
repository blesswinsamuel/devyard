import { createEffect, createMemo, createSignal, For, on, Show, type JSX } from "solid-js";
import {
  Check,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  FileCode,
  Files,
  History,
  Minus,
  Plus,
  Undo2,
  UnfoldVertical,
} from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { copyText } from "~/components/copy-button";
import { confirm } from "~/app/ui-state";
import { formatDateTime, formatRelative, now } from "~/lib/format";
import { cn } from "~/lib/utils";
import { displayPath, FileStatus, LineStats } from "./badges";
import { chunkHunks, hunkPatch, parseDiff, parseGitMeta, type ParsedDiffLine, type ParsedFileChunk } from "./diff";
import { applyHunk, CONTEXT_STEPS, FULL_CONTEXT, hunkPending, type GitCommitView, type GitFileView, type HunkAction } from "./git-data";

function CopyHash(props: { hash: string; short: string }) {
  const [done, setDone] = createSignal(false);
  return (
    <Button
      variant="outline"
      size="xs"
      class="font-mono"
      title="Copy full hash"
      aria-label={`Copy commit hash ${props.hash}`}
      onClick={async () => {
        if (await copyText(props.hash)) {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        }
      }}
    >
      <Show when={done()} fallback={<Copy />}>
        <Check class="text-success" />
      </Show>
      {props.short}
    </Button>
  );
}

/** Subject, author, date, parents and hash of a commit. */
export function CommitMeta(props: { commit: GitCommitView; onSelectCommit: (hash: string) => void }) {
  const c = () => props.commit;
  return (
    <div class="flex flex-col gap-2 sm:flex-row sm:items-start">
      <div class="flex min-w-0 flex-1 flex-col gap-1">
        <h2 class="text-sm font-semibold break-words">{c().subject}</h2>
        <Show when={c().body}>
          {(body) => (
            <pre class="max-h-40 overflow-y-auto whitespace-pre-wrap font-sans text-2xs text-muted-foreground">
              {body()}
            </pre>
          )}
        </Show>
        <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-2xs text-muted-foreground">
          <span class="font-medium text-foreground">{c().author}</span>
          <Show when={c().email}>
            <span class="truncate">&lt;{c().email}&gt;</span>
          </Show>
          <span aria-hidden="true">·</span>
          <span title={formatRelative(c().time, now())}>{formatDateTime(c().time)}</span>
          <Show when={c().parents.length}>
            <span aria-hidden="true">·</span>
            <span class="inline-flex items-center gap-1">
              {c().parents.length > 1 ? "parents" : "parent"}
              <For each={c().parents}>
                {(p) => (
                  <button
                    type="button"
                    class="focus-ring rounded-sm font-mono text-primary hover:underline"
                    title={`Go to ${p}`}
                    onClick={() => props.onSelectCommit(p)}
                  >
                    {p.slice(0, 7)}
                  </button>
                )}
              </For>
            </span>
          </Show>
        </div>
      </div>
      <CopyHash hash={c().hash} short={c().short || c().hash.slice(0, 7)} />
    </div>
  );
}

function contextLabel(n: number) {
  return n >= FULL_CONTEXT ? "Full file" : `${n} lines`;
}

/** One hunk's stage/unstage/discard controls, on its header row. */
interface HunkActions {
  project: string;
  filePath: string;
  patch: string;
  action: "stage" | "unstage";
}

function HunkButtons(props: { hunk: HunkActions }) {
  const h = () => props.hunk;
  const pending = (a: HunkAction) => hunkPending(h().project, h().filePath, a);
  const run = (a: HunkAction) => void applyHunk(h().project, h().filePath, h().patch, a);
  const discard = async () => {
    if (
      await confirm({
        title: `Discard this hunk of ${h().filePath}?`,
        description: "The change is reverted and cannot be brought back.",
        confirmLabel: "Discard hunk",
      })
    )
      run("discard");
  };
  return (
    <span class="ml-2 inline-flex items-center gap-0.5 align-middle">
      <Show when={h().action === "stage"}>
        <Button
          variant="ghost"
          size="icon-xs"
          class="size-5 text-success"
          aria-label="Stage hunk"
          title="Stage this hunk"
          disabled={pending("stage")}
          onClick={() => run("stage")}
        >
          <Show when={pending("stage")} fallback={<Plus />}>
            <Spinner class="size-3" />
          </Show>
        </Button>
        <Button
          variant="ghost"
          size="icon-xs"
          class="size-5 text-destructive"
          aria-label="Discard hunk"
          title="Discard this hunk (cannot be undone)"
          disabled={pending("discard")}
          onClick={() => void discard()}
        >
          <Show when={pending("discard")} fallback={<Undo2 />}>
            <Spinner class="size-3" />
          </Show>
        </Button>
      </Show>
      <Show when={h().action === "unstage"}>
        <Button
          variant="ghost"
          size="icon-xs"
          class="size-5 text-warning"
          aria-label="Unstage hunk"
          title="Unstage this hunk"
          disabled={pending("unstage")}
          onClick={() => run("unstage")}
        >
          <Show when={pending("unstage")} fallback={<Minus />}>
            <Spinner class="size-3" />
          </Show>
        </Button>
      </Show>
    </span>
  );
}

/** Context-lines stepper: fewer / more / full file. */
function ContextControl(props: { value: number; onChange: (n: number) => void; disabled: boolean }) {
  const idx = () => {
    const i = CONTEXT_STEPS.findIndex((s) => s >= props.value);
    return i < 0 ? CONTEXT_STEPS.length - 1 : i;
  };
  const step = (d: number) => props.onChange(CONTEXT_STEPS[Math.max(0, Math.min(CONTEXT_STEPS.length - 1, idx() + d))]!);
  return (
    <div class="flex items-center gap-0.5" role="group" aria-label="Context lines">
      <span class="hidden px-1 text-2xs text-muted-foreground sm:inline">Context</span>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label="Less context"
        disabled={props.disabled || idx() === 0}
        onClick={() => step(-1)}
      >
        <Minus />
      </Button>
      <span class="tabular min-w-14 text-center text-2xs" aria-live="polite">
        {contextLabel(props.value)}
      </span>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label="More context"
        disabled={props.disabled || idx() === CONTEXT_STEPS.length - 1}
        onClick={() => step(1)}
      >
        <Plus />
      </Button>
    </div>
  );
}

function DiffLineRow(props: { line: ParsedDiffLine; onExpand?: () => void; expanding: boolean; hunk?: HunkActions }) {
  const l = () => props.line;
  if (l().type === "hunk" || l().type === "note") {
    return (
      <tr class={cn(l().type === "hunk" ? "bg-primary/5 text-primary" : "text-muted-foreground italic")}>
        <td class="border-r select-none" colSpan={2} />
        <td class="px-3 py-0.5 whitespace-pre">
          <span class="inline-flex items-center gap-1.5">
            <Show when={l().type === "hunk" && props.onExpand}>
              <Button
                variant="ghost"
                size="icon-xs"
                class="-my-0.5 size-5 text-primary"
                aria-label="Show more context"
                title="Show more context"
                disabled={props.expanding}
                onClick={() => props.onExpand?.()}
              >
                <UnfoldVertical />
              </Button>
            </Show>
            {l().text}
          </span>
          <Show when={props.hunk}>{(h) => <HunkButtons hunk={h()} />}</Show>
        </td>
      </tr>
    );
  }
  const tone = () => (l().type === "add" ? "bg-success/10" : l().type === "delete" ? "bg-destructive/10" : "");
  const sign = () => (l().type === "add" ? "text-success" : l().type === "delete" ? "text-destructive" : "text-muted-foreground");
  return (
    <tr class={tone()}>
      <td class="tabular w-10 border-r px-1.5 text-right align-top text-2xs text-muted-foreground select-none">
        {l().oldLine ?? ""}
      </td>
      <td class="tabular w-10 border-r px-1.5 text-right align-top text-2xs text-muted-foreground select-none">
        {l().newLine ?? ""}
      </td>
      <td class="px-3 whitespace-pre">
        <span class={cn("select-none", sign())}>{l().text.slice(0, 1) || " "}</span>
        {l().text.slice(1)}
      </td>
    </tr>
  );
}

function FileDiff(props: {
  chunk: ParsedFileChunk;
  file: GitFileView | undefined;
  collapsed: boolean;
  onToggle: () => void;
  onExpand?: () => void;
  expanding: boolean;
  /** The project and whether this is the working-tree diff (hunk actions). */
  project?: string;
  workdir?: boolean;
}) {
  const meta = createMemo(() => parseGitMeta(props.chunk.metaLines));

  // Hunk actionability: only for a tracked, plainly-modified file whose
  // changes are entirely staged or entirely unstaged (a partially staged
  // file's combined diff would make a hunk ambiguous).
  const hunkAction = (): "stage" | "unstage" | null => {
    const f = props.file;
    if (!props.workdir || !props.project || !f) return null;
    if (f.untracked || meta().isNew || meta().isDeleted) return null;
    if (props.chunk.metaLines.some((l) => l.startsWith("rename from "))) return null;
    if (f.staged && f.unstaged) return null;
    return f.staged ? "unstage" : "stage";
  };
  const hunks = createMemo(() => chunkHunks(props.chunk));
  const hunkFor = (line: ParsedDiffLine): HunkActions | undefined => {
    const action = hunkAction();
    const h = hunks().find((x) => x.header === line);
    if (!action || !h || !props.project) return undefined;
    return {
      project: props.project,
      filePath: props.chunk.filePath,
      patch: hunkPatch(props.chunk, h),
      action,
    };
  };

  return (
    // content-visibility lets the browser skip rendering whole files that
    // are scrolled out of the diff pane (large diffs stay smooth) without
    // restructuring the table or losing the sticky headers.
    <section
      class="overflow-clip rounded-md border bg-card"
      aria-label={props.chunk.filePath}
      style={{ "content-visibility": "auto", "contain-intrinsic-size": "auto 600px" }}
    >
      <button
        type="button"
        class="focus-ring sticky top-0 z-10 flex w-full items-center gap-2 border-b bg-muted px-2 py-1.5 text-left"
        aria-expanded={!props.collapsed}
        onClick={props.onToggle}
      >
        <ChevronRight
          class={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", !props.collapsed && "rotate-90")}
        />
        <Show when={props.file} fallback={<FileCode class="size-3.5 shrink-0 text-muted-foreground" />}>
          {(f) => <FileStatus file={f()} />}
        </Show>
        <span class="min-w-0 flex-1 truncate text-left font-mono text-xs font-medium" dir="rtl">
          <bdi>{props.file ? displayPath(props.file) : props.chunk.filePath}</bdi>
        </span>
        <Show when={props.file}>{(f) => <LineStats additions={f().additions} deletions={f().deletions} />}</Show>
        <span class="hidden shrink-0 items-center gap-1.5 text-2xs @2xl:flex">
          <Show when={meta().modeChange}>
            {(m) => (
              <span class="rounded-sm bg-warning/15 px-1.5 text-warning">
                mode {m().oldMode} → {m().newMode}
              </span>
            )}
          </Show>
          <Show when={meta().isExecutable && !meta().modeChange}>
            <span class="rounded-sm bg-info/15 px-1.5 text-info">executable</span>
          </Show>
          <Show when={meta().blobs}>
            {(b) => (
              <span class="rounded-sm border px-1.5 font-mono text-muted-foreground">
                {b().oldHash.slice(0, 7)} → {b().newHash.slice(0, 7)}
              </span>
            )}
          </Show>
        </span>
      </button>
      <Show when={!props.collapsed}>
        <Show
          when={props.chunk.lines.length}
          fallback={
            <p class="px-3 py-2 text-2xs text-muted-foreground italic">
              {meta().modeChange
                ? `Mode changed: ${meta().modeChange!.oldMode} → ${meta().modeChange!.newMode}`
                : "No content changes."}
            </p>
          }
        >
          <div class="overflow-x-auto">
            <table class="min-w-full border-collapse font-mono text-xs">
              <tbody>
                <For each={props.chunk.lines}>
                  {(line) => (
                    <DiffLineRow
                      line={line}
                      onExpand={props.onExpand}
                      expanding={props.expanding}
                      hunk={line.type === "hunk" ? hunkFor(line) : undefined}
                    />
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
      </Show>
    </section>
  );
}

export interface DiffViewProps {
  /** Selected commit hash (resets collapse state when it changes). */
  hash: string;
  diff: string;
  files: GitFileView[];
  selectedFile: string | null;
  onShowAll: () => void;
  contextLines: number;
  onContextLines: (n: number) => void;
  loading: boolean;
  /** Commit meta or the commit box. */
  header: JSX.Element;
  /** Merge commits have no diff against their first parent here. */
  isMerge: boolean;
  /** Enables per-hunk stage/unstage/discard on the working-tree diff. */
  project?: string;
  workdir?: boolean;
  /** Extra toolbar controls (files pane toggle, mobile files tab). */
  toolbarEnd?: JSX.Element;
  /** Opens the selected file's history (commits that touched it). */
  onHistory?: (path: string) => void;
  scrollRef?: (el: HTMLDivElement) => void;
}

/** Header + toolbar + per-file unified diff. */
export function DiffView(props: DiffViewProps) {
  const chunks = createMemo(() => parseDiff(props.diff));
  const fileByPath = createMemo(() => new Map(props.files.map((f) => [f.path, f] as const)));
  const visible = createMemo(() => (props.selectedFile ? chunks().filter((c) => c.filePath === props.selectedFile) : chunks()));
  const [collapsed, setCollapsed] = createSignal<Record<string, boolean>>({});
  createEffect(
    on(
      () => props.hash,
      () => setCollapsed({}),
      { defer: true },
    ),
  );

  const totals = createMemo(() =>
    props.files.reduce((acc, f) => ({ add: acc.add + f.additions, del: acc.del + f.deletions }), { add: 0, del: 0 }),
  );
  const setAll = (value: boolean) => setCollapsed(Object.fromEntries(visible().map((c) => [c.filePath, value])));
  const nextContext = () => CONTEXT_STEPS.find((s) => s > props.contextLines);

  return (
    <div class="@container flex h-full min-h-0 flex-col">
      <div class="flex shrink-0 flex-col gap-2 border-b p-3">{props.header}</div>
      <div class="flex min-h-9 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-2 py-1">
        <Show
          when={props.selectedFile}
          fallback={
            <span class="flex items-center gap-2 px-1 text-2xs text-muted-foreground">
              <Files class="size-3.5" />
              <span class="tabular">
                {props.files.length} {props.files.length === 1 ? "file" : "files"}
              </span>
              <LineStats additions={totals().add} deletions={totals().del} />
            </span>
          }
        >
          {(path) => (
            <span class="flex min-w-0 items-center gap-1.5 px-1 text-2xs">
              <FileCode class="size-3.5 shrink-0 text-primary" />
              <span class="min-w-0 truncate font-mono">{path()}</span>
              <Show when={props.onHistory}>
                <Button
                  variant="ghost"
                  size="xs"
                  class="h-6"
                  title="History of this file"
                  onClick={() => props.onHistory?.(path())}
                >
                  <History />
                  History
                </Button>
              </Show>
              <Button variant="ghost" size="xs" class="h-6" onClick={() => props.onShowAll()}>
                Show all
              </Button>
            </span>
          )}
        </Show>
        <div class="ml-auto flex items-center gap-1">
          <Show when={props.loading}>
            <Spinner class="size-3.5 text-muted-foreground" />
          </Show>
          <ContextControl value={props.contextLines} onChange={props.onContextLines} disabled={props.loading} />
          <Show when={visible().length > 1}>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Collapse all files"
              title="Collapse all"
              onClick={() => setAll(true)}
            >
              <ChevronsDownUp />
            </Button>
            <Button variant="ghost" size="icon-xs" aria-label="Expand all files" title="Expand all" onClick={() => setAll(false)}>
              <ChevronsUpDown />
            </Button>
          </Show>
          {props.toolbarEnd}
        </div>
      </div>
      <div ref={props.scrollRef} class="focus-ring min-h-0 flex-1 overflow-auto p-2 sm:p-3" tabindex="0" aria-label="Diff">
        <Show
          when={visible().length}
          fallback={
            <p class="py-10 text-center text-ui text-muted-foreground">
              {props.isMerge ? "Merge commit without conflict resolutions: nothing to show." : "No changes."}
            </p>
          }
        >
          <div class="flex flex-col gap-3">
            <For each={visible()}>
              {(chunk) => (
                <FileDiff
                  chunk={chunk}
                  file={fileByPath().get(chunk.filePath)}
                  collapsed={!!collapsed()[chunk.filePath]}
                  onToggle={() => setCollapsed((m) => ({ ...m, [chunk.filePath]: !m[chunk.filePath] }))}
                  onExpand={nextContext() ? () => props.onContextLines(nextContext()!) : undefined}
                  expanding={props.loading}
                  project={props.project}
                  workdir={props.workdir}
                />
              )}
            </For>
          </div>
        </Show>
      </div>
    </div>
  );
}
