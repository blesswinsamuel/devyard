import { createSignal, type Accessor, type Setter } from "solid-js";
import type { PartialMessage } from "@bufbuild/protobuf";
import type { LogLine, LogSource, LogsRequest, LogsResponse } from "~/gen/devyard/v1/control_pb";
import { stripAnsi } from "~/lib/ansi";
import { backoffDelay, sleep } from "~/lib/backoff";
import { errorMessage, isCanceled } from "./errors";

// -----------------------------------------------------------------------------
// Entries
// -----------------------------------------------------------------------------

export interface LogSourceRef {
  kind: "service" | "task";
  name: string;
}

export const sourceKey = (s: LogSourceRef | LogSource) => `${s.kind}:${s.name}`;

export type LogStreamKind = "stdout" | "stderr" | "system";

export interface LogEntry {
  /** Client-side position key: strictly increasing in display order. */
  id: number;
  type: "line" | "divider";
  source: LogSourceRef;
  key: string;
  run: number;
  seq: number;
  /** Epoch milliseconds (fractional). */
  ts: number;
  stream: LogStreamKind;
  text: string;
  /** Lazily computed ANSI-stripped text (search, filter, copy). */
  plain?: string;
}

export type LogEntryInput = Omit<LogEntry, "id">;

function toEntry(line: LogLine): LogEntryInput {
  const source: LogSourceRef = {
    kind: (line.source?.kind || "service") as LogSourceRef["kind"],
    name: line.source?.name ?? "",
  };
  return {
    type: "line",
    source,
    key: sourceKey(source),
    run: Number(line.run),
    seq: Number(line.seq),
    ts: Number(line.tsUnixNanos) / 1e6,
    stream: (line.stream || "stdout") as LogStreamKind,
    text: line.text,
  };
}

function dividerFor(e: Pick<LogEntryInput, "source" | "key" | "run" | "ts">, run?: number): LogEntryInput {
  const r = run ?? e.run;
  return {
    type: "divider",
    source: e.source,
    key: e.key,
    run: r,
    seq: 0,
    ts: e.ts,
    stream: "system",
    text: r ? `run ${r} started` : "new run started",
  };
}

export const plainText = (e: LogEntry): string => (e.plain ??= stripAnsi(e.text));

// -----------------------------------------------------------------------------
// Ring buffer
// -----------------------------------------------------------------------------

/** Fixed-capacity circular buffer. Appends evict the oldest entries; prepends
 * (paging back) only fill free space so the newest lines are never lost. */
export class LogBuffer {
  private buf: (LogEntry | undefined)[];
  private head = 0;
  private len = 0;
  private nextId = 1;
  private prevId = 0;

  constructor(readonly capacity: number) {
    this.buf = new Array(capacity);
  }

  get size(): number {
    return this.len;
  }

  at(i: number): LogEntry | undefined {
    if (i < 0 || i >= this.len) return undefined;
    return this.buf[(this.head + i) % this.capacity];
  }

  first(): LogEntry | undefined {
    return this.at(0);
  }

  last(): LogEntry | undefined {
    return this.at(this.len - 1);
  }

  /** Appends entries, returning the ones evicted from the head. */
  append(inputs: LogEntryInput[]): { added: LogEntry[]; evicted: LogEntry[] } {
    const added: LogEntry[] = [];
    const evicted: LogEntry[] = [];
    // Only the newest `capacity` inputs can survive.
    const start = Math.max(0, inputs.length - this.capacity);
    this.nextId += start;
    for (let i = start; i < inputs.length; i++) {
      const entry = { ...inputs[i]!, id: this.nextId++ } as LogEntry;
      if (this.len === this.capacity) {
        evicted.push(this.buf[this.head]!);
        this.buf[this.head] = entry;
        this.head = (this.head + 1) % this.capacity;
      } else {
        this.buf[(this.head + this.len) % this.capacity] = entry;
        this.len++;
      }
      added.push(entry);
    }
    return { added, evicted };
  }

  /** Prepends chronologically ordered entries; keeps the newest that fit. */
  prepend(inputs: LogEntryInput[]): LogEntry[] {
    const room = this.capacity - this.len;
    const take = inputs.slice(Math.max(0, inputs.length - room));
    const added: LogEntry[] = [];
    for (let i = take.length - 1; i >= 0; i--) {
      const entry = { ...take[i]!, id: this.prevId-- } as LogEntry;
      this.head = (this.head - 1 + this.capacity) % this.capacity;
      this.buf[this.head] = entry;
      this.len++;
      added.unshift(entry);
    }
    return added;
  }

  clear(): void {
    this.buf = new Array(this.capacity);
    this.head = 0;
    this.len = 0;
  }

  toArray(): LogEntry[] {
    const out: LogEntry[] = [];
    for (let i = 0; i < this.len; i++) out.push(this.at(i)!);
    return out;
  }
}

// -----------------------------------------------------------------------------
// Filtered + searchable view over the buffer
// -----------------------------------------------------------------------------

export type Matcher = (text: string) => boolean;

export interface CompiledQuery {
  matcher: Matcher | null;
  /** Regex for highlighting (global, case-insensitive). */
  highlight: RegExp | null;
  error?: string;
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** Compiles a search/filter query. Plain text is case-insensitive substring. */
export function compileQuery(query: string, regex: boolean): CompiledQuery {
  if (!query) return { matcher: null, highlight: null };
  try {
    const re = new RegExp(regex ? query : escapeRe(query), "i");
    const highlight = new RegExp(re.source, "gi");
    if (!regex) {
      const needle = query.toLowerCase();
      return { matcher: (t) => t.toLowerCase().includes(needle), highlight };
    }
    return { matcher: (t) => re.test(t), highlight };
  } catch (err) {
    return { matcher: null, highlight: null, error: errorMessage(err) };
  }
}

export interface LogFilter {
  text: Matcher | null;
  hiddenSources: ReadonlySet<string>;
  hiddenStreams: ReadonlySet<LogStreamKind>;
}

const NO_FILTER: LogFilter = { text: null, hiddenSources: new Set(), hiddenStreams: new Set() };

function filterActive(f: LogFilter): boolean {
  return !!f.text || f.hiddenSources.size > 0 || f.hiddenStreams.size > 0;
}

function passes(f: LogFilter, e: LogEntry): boolean {
  if (f.hiddenSources.has(e.key)) return false;
  if (e.type === "divider") return true;
  if (f.hiddenStreams.has(e.stream)) return false;
  return !f.text || f.text(plainText(e));
}

/** Result of a mutation, in visible rows, for scroll anchoring. */
export interface ViewChange {
  appended: number;
  evicted: number;
  prepended: number;
  /** Search hits dropped from the head / added at the head (hit indices shift). */
  hitsEvicted: number;
  hitsPrepended: number;
}

const NO_CHANGE: ViewChange = { appended: 0, evicted: 0, prepended: 0, hitsEvicted: 0, hitsPrepended: 0 };

/**
 * The buffer plus a filtered row list and incremental search hits. All ids
 * are increasing in display order, so id → row lookups are binary searches.
 */
export class LogModel {
  readonly buffer: LogBuffer;
  private filter: LogFilter = NO_FILTER;
  private rows: LogEntry[] | null = null;
  private search: Matcher | null = null;
  private hits: number[] = [];

  constructor(capacity: number) {
    this.buffer = new LogBuffer(capacity);
  }

  get count(): number {
    return this.rows ? this.rows.length : this.buffer.size;
  }

  at(i: number): LogEntry | undefined {
    return this.rows ? this.rows[i] : this.buffer.at(i);
  }

  /** Row index of the entry with `id`, or the first row after it. */
  indexOfId(id: number): number {
    let lo = 0;
    let hi = this.count;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.at(mid)!.id < id) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  append(inputs: LogEntryInput[]): ViewChange {
    const { added, evicted } = this.buffer.append(inputs);
    let evictedRows = evicted.length;
    let appendedRows = added.length;
    if (this.rows) {
      const firstId = this.buffer.first()?.id ?? Infinity;
      let drop = 0;
      while (drop < this.rows.length && this.rows[drop]!.id < firstId) drop++;
      if (drop) this.rows.splice(0, drop);
      evictedRows = drop;
      appendedRows = 0;
      for (const e of added) {
        if (passes(this.filter, e)) {
          this.rows.push(e);
          appendedRows++;
        }
      }
    }
    let hitsEvicted = 0;
    if (evicted.length) {
      const firstId = this.buffer.first()?.id ?? Infinity;
      while (hitsEvicted < this.hits.length && this.hits[hitsEvicted]! < firstId) hitsEvicted++;
      if (hitsEvicted) this.hits.splice(0, hitsEvicted);
    }
    if (this.search) for (const e of added) if (this.isHit(e)) this.hits.push(e.id);
    return { ...NO_CHANGE, appended: appendedRows, evicted: evictedRows, hitsEvicted };
  }

  prepend(inputs: LogEntryInput[]): ViewChange {
    const added = this.buffer.prepend(inputs);
    let rows = added.length;
    if (this.rows) {
      const visible = added.filter((e) => passes(this.filter, e));
      this.rows.unshift(...visible);
      rows = visible.length;
    }
    const newHits = this.search ? added.filter((e) => this.isHit(e)).map((e) => e.id) : [];
    this.hits.unshift(...newHits);
    return { ...NO_CHANGE, prepended: rows, hitsPrepended: newHits.length };
  }

  clear(): void {
    this.buffer.clear();
    if (this.rows) this.rows = [];
    this.hits = [];
  }

  setFilter(filter: LogFilter): void {
    this.filter = filter;
    this.rows = filterActive(filter) ? this.buffer.toArray().filter((e) => passes(filter, e)) : null;
    this.rebuildHits();
  }

  setSearch(matcher: Matcher | null): void {
    this.search = matcher;
    this.rebuildHits();
  }

  get hitCount(): number {
    return this.hits.length;
  }

  hitId(i: number): number | undefined {
    return this.hits[i];
  }

  /** Index into hits of the first hit with id >= `id` (wrapping to 0). */
  hitIndexFrom(id: number): number {
    let lo = 0;
    let hi = this.hits.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.hits[mid]! < id) lo = mid + 1;
      else hi = mid;
    }
    return lo >= this.hits.length ? 0 : lo;
  }

  /** Plain text of every visible row (copy / download). */
  text(): string {
    const lines: string[] = [];
    for (let i = 0; i < this.count; i++) {
      const e = this.at(i)!;
      lines.push(e.type === "divider" ? `--- ${e.source.name}: ${e.text} ---` : plainText(e));
    }
    return lines.join("\n");
  }

  private isHit(e: LogEntry): boolean {
    return e.type === "line" && !!this.search && passes(this.filter, e) && this.search(plainText(e));
  }

  private rebuildHits(): void {
    this.hits = [];
    if (!this.search) return;
    for (let i = 0; i < this.count; i++) {
      const e = this.at(i)!;
      if (this.isHit(e)) this.hits.push(e.id);
    }
  }
}

// -----------------------------------------------------------------------------
// Ordering helpers
// -----------------------------------------------------------------------------

/** Tracks the last (run, seq) per source so reconnect tails don't duplicate. */
export class LastSeen {
  private seen = new Map<string, { run: number; seq: number }>();

  /** "first" = source never seen; "new-run" = a later run began. */
  accept(e: Pick<LogEntryInput, "key" | "run" | "seq">): "dup" | "first" | "new-run" | "ok" {
    const prev = this.seen.get(e.key);
    if (prev && (e.run < prev.run || (e.run === prev.run && e.seq <= prev.seq))) return "dup";
    this.seen.set(e.key, { run: e.run, seq: e.seq });
    if (!prev) return "first";
    return e.run > prev.run ? "new-run" : "ok";
  }

  latestRun(key: string): number | undefined {
    return this.seen.get(key)?.run;
  }

  /** Records history loaded by paging so it never lowers the high-water mark. */
  note(e: Pick<LogEntryInput, "key" | "run" | "seq">): void {
    const prev = this.seen.get(e.key);
    if (!prev || e.run > prev.run || (e.run === prev.run && e.seq > prev.seq))
      this.seen.set(e.key, { run: e.run, seq: e.seq });
  }
}

const compareEntries = (a: LogEntryInput, b: LogEntryInput) =>
  a.ts - b.ts || a.key.localeCompare(b.key) || a.run - b.run || a.seq - b.seq;

/**
 * Merges per-source pages of older history into the `limit` lines that
 * immediately precede `boundary` (the buffer's oldest line) in merged order.
 */
export function mergeOlder(pages: LogEntryInput[][], limit: number, boundary: LogEntryInput): LogEntryInput[] {
  const all = pages.flat().filter((e) => compareEntries(e, boundary) < 0);
  all.sort(compareEntries);
  return all.slice(Math.max(0, all.length - limit));
}

// -----------------------------------------------------------------------------
// Streaming client
// -----------------------------------------------------------------------------

export interface LogStreamParams {
  project: string;
  /** Empty = every service of the project, merged by timestamp. */
  sources: LogSourceRef[];
  /** 0 = current run, -1 = previous, … */
  runOffset: number;
  follow: boolean;
  tail?: number;
}

export interface LogsClient {
  logs(req: PartialMessage<LogsRequest>, opts: { signal: AbortSignal }): AsyncIterable<LogsResponse>;
}

export type LogStreamPhase = "loading" | "live" | "done" | "reconnecting" | "error";

export interface LogStreamOptions {
  client: LogsClient;
  capacity?: number;
  /** Sources to page back through in a merged view (defaults to seen sources). */
  knownSources?: () => LogSourceRef[];
  /** Coalesces appends; defaults to one flush per animation frame. */
  schedule?: (cb: () => void) => void;
  random?: () => number;
}

const DEFAULT_CAPACITY = 50_000;
const DEFAULT_TAIL = 2_000;
const PAGE_SIZE = 1_000;
const RECONNECT_TAIL = 2_000;
const MAX_SEQ = 18446744073709551615n;

const frame = (cb: () => void) =>
  typeof requestAnimationFrame === "function" ? requestAnimationFrame(cb) : setTimeout(cb, 16);

export class LogStream {
  readonly model: LogModel;
  readonly phase: Accessor<LogStreamPhase>;
  readonly hasMoreBefore: Accessor<boolean>;
  readonly loadingOlder: Accessor<boolean>;
  readonly error: Accessor<string>;
  /** Bumped after every visible change; read it to subscribe. */
  readonly version: Accessor<number>;

  private setPhase: Setter<LogStreamPhase>;
  private setHasMoreBefore: Setter<boolean>;
  private setLoadingOlder: Setter<boolean>;
  private setError: Setter<string>;
  private setVersion: Setter<number>;
  private pending: LogEntryInput[] = [];
  private scheduled = false;
  private disposed = false;
  private abort = new AbortController();
  private lastSeen = new LastSeen();
  private listeners = new Set<(c: ViewChange) => void>();

  constructor(
    readonly params: LogStreamParams,
    private opts: LogStreamOptions,
  ) {
    this.model = new LogModel(opts.capacity ?? DEFAULT_CAPACITY);
    [this.phase, this.setPhase] = createSignal<LogStreamPhase>("loading");
    [this.hasMoreBefore, this.setHasMoreBefore] = createSignal(false);
    [this.loadingOlder, this.setLoadingOlder] = createSignal(false);
    [this.error, this.setError] = createSignal("");
    [this.version, this.setVersion] = createSignal(0, { equals: false });
  }

  onChange(listener: (c: ViewChange) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Re-derives rows after a filter/search change (no network). */
  refresh(change: ViewChange = NO_CHANGE): void {
    this.listeners.forEach((l) => l(change));
    this.setVersion(0);
  }

  start(): void {
    void this.run();
  }

  dispose(): void {
    this.disposed = true;
    this.abort.abort();
  }

  clear(): void {
    this.pending = [];
    this.model.clear();
    this.setHasMoreBefore(false);
    this.refresh();
  }

  private request(tail: number): PartialMessage<LogsRequest> {
    return {
      project: this.params.project,
      sources: this.params.sources,
      runOffset: BigInt(this.params.runOffset),
      tail,
      follow: this.params.follow,
    };
  }

  private async run(): Promise<void> {
    let attempt = 0;
    let initial = true;
    while (!this.disposed) {
      try {
        let first = true;
        const tail = initial ? (this.params.tail ?? DEFAULT_TAIL) : RECONNECT_TAIL;
        for await (const res of this.opts.client.logs(this.request(tail), { signal: this.abort.signal })) {
          if (first) {
            first = false;
            attempt = 0;
            if (initial) this.setHasMoreBefore(res.hasMoreBefore);
            this.setError("");
            this.setPhase(this.params.follow ? "live" : "loading");
          }
          this.ingest(res);
        }
        if (!this.params.follow) {
          this.flush();
          this.setPhase("done");
          return;
        }
        throw new Error("log stream ended");
      } catch (err) {
        if (this.disposed || isCanceled(err)) return;
        this.setError(errorMessage(err));
        if (!this.params.follow) {
          this.flush();
          this.setPhase("error");
          return;
        }
        this.setPhase("reconnecting");
        attempt++;
        await sleep(backoffDelay(attempt, { baseMs: 500, maxMs: 8000, random: this.opts.random }), this.abort.signal);
      }
      initial = false;
    }
  }

  /** Visible for tests: ingests one response batch. */
  ingest(res: Pick<LogsResponse, "lines" | "newRun">): void {
    const newRun = new Set(res.newRun.map(sourceKey));
    const divided = new Set<string>();
    for (const line of res.lines) {
      const e = toEntry(line);
      const verdict = this.lastSeen.accept(e);
      if (verdict === "dup") continue;
      if ((newRun.has(e.key) || verdict === "new-run") && !divided.has(e.key)) {
        divided.add(e.key);
        this.pending.push(dividerFor(e));
      }
      this.pending.push(e);
    }
    for (const key of newRun) {
      if (divided.has(key)) continue;
      const [kind, ...rest] = key.split(":");
      const source = { kind: kind as LogSourceRef["kind"], name: rest.join(":") };
      this.pending.push(dividerFor({ source, key, run: 0, ts: Date.now() }));
    }
    this.scheduleFlush();
  }

  private scheduleFlush(): void {
    if (this.scheduled || !this.pending.length) return;
    this.scheduled = true;
    (this.opts.schedule ?? frame)(() => {
      this.scheduled = false;
      this.flush();
    });
  }

  flush(): void {
    if (!this.pending.length || this.disposed) return;
    const batch = this.pending;
    this.pending = [];
    this.refresh(this.model.append(batch));
  }

  /** Run offset (relative to the source's latest run) for paging `e`'s run. */
  private offsetFor(key: string, run: number): number {
    if (!this.params.follow) return this.params.runOffset;
    const latest = this.lastSeen.latestRun(key) ?? run;
    return run - latest;
  }

  private oldestLine(): LogEntry | undefined {
    const b = this.model.buffer;
    for (let i = 0; i < b.size; i++) {
      const e = b.at(i)!;
      if (e.type === "line") return e;
    }
    return undefined;
  }

  private oldestPerSource(): Map<string, LogEntry> {
    const map = new Map<string, LogEntry>();
    const b = this.model.buffer;
    for (let i = 0; i < b.size; i++) {
      const e = b.at(i)!;
      if (e.type === "line" && !map.has(e.key)) map.set(e.key, e);
    }
    return map;
  }

  private async page(source: LogSourceRef, runOffset: number, beforeSeq: bigint): Promise<{
    entries: LogEntryInput[];
    hasMore: boolean;
  }> {
    const entries: LogEntryInput[] = [];
    let hasMore = false;
    let first = true;
    for await (const res of this.opts.client.logs(
      {
        project: this.params.project,
        sources: [source],
        runOffset: BigInt(runOffset),
        tail: PAGE_SIZE,
        beforeSeq,
        follow: false,
      },
      { signal: this.abort.signal },
    )) {
      if (first) {
        first = false;
        hasMore = res.hasMoreBefore;
      }
      for (const line of res.lines) entries.push(toEntry(line));
    }
    return { entries, hasMore };
  }

  /** Loads the page of history before the oldest buffered line. */
  async loadOlder(): Promise<void> {
    if (this.loadingOlder() || !this.hasMoreBefore() || this.disposed) return;
    const oldest = this.oldestLine();
    if (!oldest) return;
    this.setLoadingOlder(true);
    try {
      const explicit = this.params.sources;
      let older: LogEntryInput[];
      let hasMore: boolean;
      if (explicit.length === 1) {
        const r = await this.page(explicit[0]!, this.offsetFor(oldest.key, oldest.run), BigInt(oldest.seq));
        older = r.entries.filter((e) => e.run === oldest.run && e.seq < oldest.seq);
        hasMore = r.hasMore;
      } else {
        // Merged view: page every source back from its own oldest line and
        // keep the PAGE_SIZE lines just before the merged boundary.
        const perSource = this.oldestPerSource();
        const sources = explicit.length ? explicit : (this.opts.knownSources?.() ?? []);
        const all = new Map(sources.map((s) => [sourceKey(s), s] as const));
        for (const e of perSource.values()) all.set(e.key, e.source);
        const results = await Promise.all(
          [...all].map(([key, source]) => {
            const o = perSource.get(key);
            return o
              ? this.page(source, this.offsetFor(key, o.run), BigInt(o.seq))
              : this.page(source, this.params.runOffset, MAX_SEQ);
          }),
        );
        const fetched = results.reduce((n, r) => n + r.entries.length, 0);
        older = mergeOlder(
          results.map((r) => r.entries),
          PAGE_SIZE,
          oldest,
        );
        hasMore = results.some((r) => r.hasMore) || fetched > older.length;
      }
      if (this.disposed) return;
      older.forEach((e) => this.lastSeen.note(e));
      const change = this.model.prepend(older);
      this.setHasMoreBefore(hasMore && older.length > 0);
      this.refresh(change);
    } catch (err) {
      if (!isCanceled(err) && !this.disposed) this.setError(errorMessage(err));
    } finally {
      this.setLoadingOlder(false);
    }
  }
}
