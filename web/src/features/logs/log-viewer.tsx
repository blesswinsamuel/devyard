import {
  batch,
  createEffect,
  createMemo,
  createSignal,
  For,
  on,
  onCleanup,
  Show,
  untrack,
  type JSX,
} from "solid-js";
import { createVirtualizer } from "@tanstack/solid-virtual";
import {
  ArrowDown,
  ChevronDown,
  ChevronUp,
  Clock,
  Copy,
  Download,
  Eraser,
  Funnel,
  LoaderCircle,
  Regex,
  Search,
  WrapText,
} from "lucide-solid";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "~/components/ui/input-group";
import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { Toggle } from "~/components/ui/toggle";
import { CheckboxField } from "~/components/checkbox-field";
import { copyText } from "~/components/copy-button";
import { api } from "~/data/client";
import { servicesOf } from "~/data/entities";
import {
  compileQuery,
  LogStream,
  plainText,
  sourceKey,
  type LogEntry,
  type LogSourceRef,
  type LogStreamKind,
  type ViewChange,
} from "~/data/logs";
import { sourceColor } from "~/lib/color";
import { formatClock } from "~/lib/format";
import { isMac } from "~/lib/keyboard";
import { createPersistedSignal } from "~/lib/persistence";
import { cn } from "~/lib/utils";
import { LogText } from "./log-text";

const ROW_H = 18;
const LOAD_OLDER_THRESHOLD = 240;

interface LogPrefs {
  wrap: boolean;
  timestamps: boolean;
}

const [prefs, setPrefs] = createPersistedSignal<LogPrefs>("logs.prefs", { wrap: false, timestamps: true }, (raw) =>
  raw && typeof raw === "object" && "wrap" in raw && "timestamps" in raw
    ? { wrap: !!(raw as LogPrefs).wrap, timestamps: !!(raw as LogPrefs).timestamps }
    : undefined,
);

const STREAMS: LogStreamKind[] = ["stdout", "stderr", "system"];

export interface LogViewerProps {
  project: string;
  /** Empty = all services of the project, merged. */
  sources: LogSourceRef[];
  runOffset?: number;
  /** Defaults to following when viewing the current run. */
  follow?: boolean;
  /** Sources offered as filter chips (merged views). */
  chipSources?: LogSourceRef[];
  toolbarStart?: JSX.Element;
  toolbarEnd?: JSX.Element;
  emptyText?: string;
  class?: string;
  /** Accessible name of the log region. */
  label: string;
}

export function LogViewer(props: LogViewerProps) {
  const sourcesKey = createMemo(() => props.sources.map(sourceKey).join(","));
  const runOffset = () => props.runOffset ?? 0;
  const follow = () => props.follow ?? runOffset() === 0;
  const merged = () => props.sources.length !== 1;

  const stream = createMemo(() => {
    sourcesKey();
    const project = props.project;
    const s = new LogStream(
      { project, sources: untrack(() => props.sources), runOffset: runOffset(), follow: follow() },
      {
        client: api,
        knownSources: () => servicesOf(project).map((svc) => ({ kind: "service" as const, name: svc.name })),
      },
    );
    s.start();
    onCleanup(() => s.dispose());
    return s;
  });

  // ------------------------------------------------------------ filter/search
  const [searchText, setSearchText] = createSignal("");
  const [searchRegex, setSearchRegex] = createSignal(false);
  const [filterText, setFilterText] = createSignal("");
  const [filterRegex, setFilterRegex] = createSignal(false);
  const [hiddenSources, setHiddenSources] = createSignal<ReadonlySet<string>>(new Set());
  const [hiddenStreams, setHiddenStreams] = createSignal<ReadonlySet<LogStreamKind>>(new Set());
  const [hit, setHit] = createSignal(-1);

  const search = createMemo(() => compileQuery(searchText(), searchRegex()));
  const filter = createMemo(() => compileQuery(filterText(), filterRegex()));
  const filtersActive = () => !!filter().matcher || hiddenStreams().size > 0;

  createEffect(() => {
    const s = stream();
    s.model.setFilter({ text: filter().matcher, hiddenSources: hiddenSources(), hiddenStreams: hiddenStreams() });
    s.model.setSearch(search().matcher);
    untrack(() => setHit(-1));
    s.refresh();
  });

  const count = createMemo(() => {
    stream().version();
    return stream().model.count;
  });
  const hitCount = createMemo(() => {
    stream().version();
    return stream().model.hitCount;
  });
  const currentHitId = createMemo(() => {
    stream().version();
    const i = hit();
    return i >= 0 ? stream().model.hitId(i) : undefined;
  });

  // ----------------------------------------------------------------- scrolling
  let scrollEl!: HTMLDivElement;
  let searchInput: HTMLInputElement | undefined;
  const [stick, setStick] = createSignal(true);
  const [unseen, setUnseen] = createSignal(0);
  const [nearTop, setNearTop] = createSignal(false);
  let lastTop = 0;

  const virtualizer = createVirtualizer({
    get count() {
      return count();
    },
    getScrollElement: () => scrollEl,
    estimateSize: () => ROW_H,
    overscan: 24,
    getItemKey: (i: number) => untrack(() => stream().model.at(i)?.id ?? i),
  });

  createEffect(
    on(stream, (s) => {
      batch(() => {
        setStick(true);
        setUnseen(0);
      });
      const off = s.onChange((c: ViewChange) => {
        if (c.evicted && !untrack(stick)) {
          // Keep the visible lines in place while the head is trimmed.
          let removed = 0;
          const cache = virtualizer.measurementsCache;
          for (let i = 0; i < c.evicted; i++) removed += cache[i]?.size ?? ROW_H;
          scrollEl.scrollTop = Math.max(0, scrollEl.scrollTop - removed);
          lastTop = scrollEl.scrollTop;
        }
        if (c.prepended) {
          scrollEl.scrollTop += c.prepended * ROW_H;
          lastTop = scrollEl.scrollTop;
        }
        if (c.appended && !untrack(stick)) setUnseen((n) => n + c.appended);
        // Keep the current match on the same line as hits shift.
        if (c.hitsEvicted || c.hitsPrepended)
          setHit((h) => (h < 0 ? h : h - c.hitsEvicted < 0 ? -1 : h - c.hitsEvicted + c.hitsPrepended));
      });
      onCleanup(off);
    }),
  );

  // Follow the tail after every render that grew the list.
  createEffect(
    on(
      () => virtualizer.getTotalSize(),
      () => {
        if (!scrollEl) return;
        if (stick()) {
          scrollEl.scrollTop = scrollEl.scrollHeight;
          lastTop = scrollEl.scrollTop;
        }
        setNearTop(scrollEl.scrollTop < LOAD_OLDER_THRESHOLD);
      },
    ),
  );

  const onScroll = () => {
    const top = scrollEl.scrollTop;
    const atBottom = scrollEl.scrollHeight - top - scrollEl.clientHeight < 4;
    if (atBottom) {
      if (!stick()) batch(() => (setStick(true), setUnseen(0)));
    } else if (top < lastTop - 2) setStick(false);
    lastTop = top;
    setNearTop(top < LOAD_OLDER_THRESHOLD);
    if (top < LOAD_OLDER_THRESHOLD && stream().hasMoreBefore() && !stream().loadingOlder()) void stream().loadOlder();
  };

  const jumpToLatest = () => {
    batch(() => (setStick(true), setUnseen(0)));
    scrollEl.scrollTop = scrollEl.scrollHeight;
  };

  const goToHit = (dir: 1 | -1) => {
    const n = hitCount();
    if (!n) return;
    let i = hit();
    if (i < 0) {
      // Start from the viewport: next hit below the first visible row.
      const first = virtualizer.getVirtualItems()[0]?.index ?? 0;
      const id = stream().model.at(first)?.id ?? 0;
      i = stream().model.hitIndexFrom(id);
      if (dir < 0) i = (i - 1 + n) % n;
    } else i = (i + dir + n) % n;
    setHit(i);
    setStick(false);
    const id = stream().model.hitId(i)!;
    virtualizer.scrollToIndex(stream().model.indexOfId(id), { align: "center" });
  };

  // --------------------------------------------------------------- rendering
  const showSource = () => merged();
  const sourceWidth = createMemo(() => {
    const names = (props.chipSources ?? []).map((s) => s.name.length);
    return Math.min(18, Math.max(6, ...names)) + 1;
  });
  const rowAt = (index: number): LogEntry | undefined => {
    stream().version();
    return stream().model.at(index);
  };

  const toggleSource = (key: string, solo: boolean) => {
    const all = (props.chipSources ?? []).map(sourceKey);
    setHiddenSources((prev) => {
      if (solo) {
        const onlyThis = all.every((k) => (k === key ? !prev.has(k) : prev.has(k)));
        return onlyThis ? new Set<string>() : new Set(all.filter((k) => k !== key));
      }
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const download = () => {
    const blob = new Blob([stream().model.text()], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${props.project}-${props.sources.map((s) => s.name).join("-") || "all"}.log`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  const onRootKey = (e: KeyboardEvent) => {
    const mod = isMac ? e.metaKey : e.ctrlKey;
    if ((mod && e.key.toLowerCase() === "f") || (e.key === "/" && e.target === scrollEl)) {
      e.preventDefault();
      searchInput?.focus();
      searchInput?.select();
    } else if (e.key === "End" && e.target === scrollEl) {
      e.preventDefault();
      jumpToLatest();
    }
  };

  const phaseLabel = () => {
    const s = stream();
    switch (s.phase()) {
      case "loading":
        return "Loading…";
      case "reconnecting":
        return "Reconnecting…";
      case "error":
        return "Error";
      case "live":
        return "Live";
      default:
        return "History";
    }
  };

  return (
    <section
      class={cn("flex min-h-0 flex-col overflow-hidden rounded-lg border bg-terminal text-terminal-foreground", props.class)}
      aria-label={props.label}
      onKeyDown={onRootKey}
    >
      {/* Toolbar */}
      <div class="flex flex-wrap items-center gap-1.5 border-b bg-card px-2 py-1.5 text-card-foreground">
        {props.toolbarStart}
        <InputGroup class="h-7 w-full max-w-64 min-w-40 flex-1 sm:flex-none">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            ref={searchInput}
            placeholder="Search"
            aria-label="Search logs"
            aria-invalid={!!search().error || undefined}
            value={searchText()}
            onInput={(e) => setSearchText(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                goToHit(e.shiftKey ? -1 : 1);
              } else if (e.key === "Escape") {
                setSearchText("");
                scrollEl.focus();
              }
            }}
          />
          <InputGroupAddon align="inline-end" class="gap-0.5">
            <Show when={searchText()}>
              <span class="tabular px-1 text-2xs text-muted-foreground" aria-live="polite">
                {search().error ? "invalid" : `${hit() >= 0 ? hit() + 1 : 0}/${hitCount()}`}
              </span>
            </Show>
            <InputGroupButton
              size="icon-xs"
              aria-label="Regular expression"
              aria-pressed={searchRegex()}
              class={searchRegex() ? "text-primary" : undefined}
              onClick={() => setSearchRegex((v) => !v)}
            >
              <Regex />
            </InputGroupButton>
            <InputGroupButton size="icon-xs" aria-label="Previous match" onClick={() => goToHit(-1)} disabled={!hitCount()}>
              <ChevronUp />
            </InputGroupButton>
            <InputGroupButton size="icon-xs" aria-label="Next match" onClick={() => goToHit(1)} disabled={!hitCount()}>
              <ChevronDown />
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>

        <Popover>
          <PopoverTrigger
            as={Button}
            variant={filtersActive() ? "secondary" : "ghost"}
            size="sm"
            class={cn("h-7", filtersActive() && "text-primary")}
            aria-label="Filter lines"
          >
            <Funnel />
            <span class="hidden sm:inline">Filter</span>
          </PopoverTrigger>
          <PopoverContent class="w-72">
            <div class="flex flex-col gap-3">
              <InputGroup class="h-8">
                <InputGroupInput
                  placeholder={filterRegex() ? "Regex, e.g. error|warn" : "Only lines containing…"}
                  aria-label="Filter text"
                  aria-invalid={!!filter().error || undefined}
                  value={filterText()}
                  onInput={(e) => setFilterText(e.currentTarget.value)}
                />
                <InputGroupAddon align="inline-end">
                  <InputGroupButton
                    size="icon-xs"
                    aria-label="Regular expression"
                    aria-pressed={filterRegex()}
                    class={filterRegex() ? "text-primary" : undefined}
                    onClick={() => setFilterRegex((v) => !v)}
                  >
                    <Regex />
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
              <Show when={filter().error}>
                <p class="text-2xs text-destructive">{filter().error}</p>
              </Show>
              <fieldset class="flex flex-wrap gap-3">
                <legend class="mb-1.5 text-2xs font-medium text-muted-foreground">Streams</legend>
                <For each={STREAMS}>
                  {(s) => (
                    <CheckboxField
                      label={s}
                      checked={!hiddenStreams().has(s)}
                      onChange={(on: boolean) =>
                        setHiddenStreams((prev) => {
                          const next = new Set(prev);
                          if (on) next.delete(s);
                          else next.add(s);
                          return next;
                        })
                      }
                    />
                  )}
                </For>
              </fieldset>
              <Show when={filtersActive()}>
                <Button
                  variant="ghost"
                  size="sm"
                  class="self-start"
                  onClick={() => batch(() => (setFilterText(""), setHiddenStreams(new Set())))}
                >
                  Clear filters
                </Button>
              </Show>
            </div>
          </PopoverContent>
        </Popover>

        <Show when={(props.chipSources?.length ?? 0) > 1}>
          <div class="flex min-w-0 flex-wrap items-center gap-1" role="group" aria-label="Sources">
            <For each={props.chipSources}>
              {(src) => {
                const key = sourceKey(src);
                const on = () => !hiddenSources().has(key);
                return (
                  <button
                    type="button"
                    class={cn(
                      "focus-ring inline-flex h-6 items-center gap-1.5 rounded-md border px-2 text-2xs transition-colors",
                      on() ? "bg-background text-foreground" : "border-dashed text-muted-foreground line-through opacity-70",
                    )}
                    aria-pressed={on()}
                    title="Click to toggle, Shift+click to show only this source"
                    onClick={(e) => toggleSource(key, e.shiftKey)}
                  >
                    <span class="size-2 rounded-full" style={{ background: sourceColor(src.name) }} />
                    {src.name}
                  </button>
                );
              }}
            </For>
          </div>
        </Show>

        <div class="ml-auto flex items-center gap-1">
          <span class="hidden items-center gap-1.5 px-1 text-2xs text-muted-foreground sm:inline-flex" aria-live="polite">
            <Show
              when={stream().phase() === "live"}
              fallback={
                <Show when={stream().phase() === "loading" || stream().phase() === "reconnecting"}>
                  <LoaderCircle class="size-3 animate-spin" />
                </Show>
              }
            >
              <span class="size-1.5 rounded-full bg-success" />
            </Show>
            {phaseLabel()}
            <span class="tabular">· {count().toLocaleString()} lines</span>
          </span>
          <Toggle
            size="sm"
            class="h-7"
            pressed={prefs().wrap}
            onPressedChange={(v: boolean) => setPrefs({ ...prefs(), wrap: v })}
            aria-label="Wrap lines"
            title="Wrap lines"
          >
            <WrapText />
          </Toggle>
          <Toggle
            size="sm"
            class="h-7"
            pressed={prefs().timestamps}
            onPressedChange={(v: boolean) => setPrefs({ ...prefs(), timestamps: v })}
            aria-label="Show timestamps"
            title="Show timestamps"
          >
            <Clock />
          </Toggle>
          <DropdownMenu>
            <DropdownMenuTrigger as={Button} variant="ghost" size="icon-sm" aria-label="More log actions">
              <Copy />
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => void copyText(stream().model.text())}>
                <Copy />
                Copy visible lines
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={download}>
                <Download />
                Download .log
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => stream().clear()}>
                <Eraser />
                Clear view
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          {props.toolbarEnd}
        </div>
      </div>

      {/* Body */}
      <div class="relative min-h-0 flex-1">
        <div
          ref={scrollEl}
          class={cn("focus-ring absolute inset-0 overflow-auto font-mono text-xs", prefs().wrap ? "overflow-x-hidden" : "")}
          tabindex="0"
          role="log"
          aria-label={`${props.label} output`}
          aria-live="off"
          onScroll={onScroll}
        >
          <div class="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
            <For each={virtualizer.getVirtualItems()}>
              {(item) => (
                <div
                  data-index={item.index}
                  ref={(el) => {
                    if (prefs().wrap) queueMicrotask(() => el.isConnected && virtualizer.measureElement(el));
                  }}
                  class="absolute left-0 min-w-full"
                  style={{
                    transform: `translateY(${item.start}px)`,
                    height: prefs().wrap ? undefined : `${ROW_H}px`,
                    width: prefs().wrap ? "100%" : undefined,
                  }}
                >
                  <Show when={rowAt(item.index)} keyed>
                    {(entry) => (
                      <LogRow
                        entry={entry}
                        wrap={prefs().wrap}
                        timestamps={prefs().timestamps}
                        showSource={showSource()}
                        sourceWidth={sourceWidth()}
                        highlight={search().highlight}
                        current={currentHitId() === entry.id}
                      />
                    )}
                  </Show>
                </div>
              )}
            </For>
          </div>
          <Show when={count() === 0 && stream().phase() !== "loading"}>
            <div class="absolute inset-0 flex items-center justify-center p-6 text-center font-sans text-ui text-muted-foreground">
              <Show
                when={stream().phase() !== "error"}
                fallback={<span class="text-destructive">Couldn't load logs: {stream().error()}</span>}
              >
                {filtersActive() || hiddenSources().size ? "No lines match the filter." : (props.emptyText ?? "No output yet.")}
              </Show>
            </div>
          </Show>
          <Show when={count() === 0 && stream().phase() === "loading"}>
            <div class="absolute inset-0 flex items-center justify-center gap-2 font-sans text-ui text-muted-foreground">
              <LoaderCircle class="size-4 animate-spin" /> Loading logs…
            </div>
          </Show>
        </div>
        <Show when={stream().loadingOlder() || (nearTop() && stream().hasMoreBefore() && count() > 0)}>
          <div class="pointer-events-none absolute inset-x-0 top-1.5 z-10 flex justify-center">
            <button
              type="button"
              class="focus-ring pointer-events-auto inline-flex items-center gap-1.5 rounded-full border bg-popover px-2 py-0.5 text-2xs text-muted-foreground shadow-sm hover:text-foreground"
              disabled={stream().loadingOlder()}
              onClick={() => void stream().loadOlder()}
            >
              <Show when={stream().loadingOlder()} fallback="Load earlier lines">
                <LoaderCircle class="size-3 animate-spin" />
                Loading earlier lines…
              </Show>
            </button>
          </div>
        </Show>
        <Show when={!stick() && follow()}>
          <Button
            size="sm"
            class="absolute right-4 bottom-3 z-10 rounded-full shadow-md"
            onClick={jumpToLatest}
          >
            <ArrowDown />
            {unseen() ? `${unseen().toLocaleString()} new` : "Jump to latest"}
          </Button>
        </Show>
      </div>
    </section>
  );
}

function LogRow(props: {
  entry: LogEntry;
  wrap: boolean;
  timestamps: boolean;
  showSource: boolean;
  sourceWidth: number;
  highlight: RegExp | null;
  current: boolean;
}) {
  const e = props.entry;
  if (e.type === "divider") {
    return (
      <div class="flex h-full items-center gap-2 px-3 font-sans text-2xs text-muted-foreground" role="separator">
        <span class="h-px flex-1 bg-border" />
        <span style={{ color: sourceColor(e.source.name) }}>{e.source.name}</span>
        <span>— {e.text} —</span>
        <span class="h-px flex-1 bg-border" />
      </div>
    );
  }
  return (
    <div
      class={cn(
        "group flex min-h-full items-start gap-3 px-3 leading-4.5",
        props.wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre",
        e.stream === "stderr" && "bg-log-stderr",
        e.stream === "system" && "text-info italic",
        props.current && "bg-log-match",
      )}
    >
      <Show when={props.timestamps}>
        <span class="tabular shrink-0 text-muted-foreground select-none">{formatClock(e.ts)}</span>
      </Show>
      <Show when={props.showSource}>
        <span
          class="shrink-0 truncate select-none"
          style={{ color: sourceColor(e.source.name), width: `${props.sourceWidth}ch` }}
          title={e.source.name}
        >
          {e.source.name}
        </span>
      </Show>
      <span class="min-w-0 flex-1">
        <Show when={e.stream === "system"}>
          <span class="select-none" aria-hidden="true">
            ›{" "}
          </span>
        </Show>
        <LogText entry={e} highlight={props.highlight} current={props.current} />
      </span>
      <button
        type="button"
        class="sticky right-1 shrink-0 self-center rounded-sm px-1 font-sans text-2xs text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100 hover:text-foreground"
        aria-label="Copy line"
        onClick={() => void copyText(plainText(e))}
      >
        copy
      </button>
    </div>
  );
}
