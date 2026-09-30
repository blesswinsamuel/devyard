import { describe, expect, it, vi } from "vitest";
import { LogLine, LogSource, LogsResponse, type LogsRequest } from "~/gen/devyard/v1/control_pb";
import type { PartialMessage } from "@bufbuild/protobuf";
import {
  LastSeen,
  LogBuffer,
  LogModel,
  LogStream,
  compileQuery,
  mergeOlder,
  sourceKey,
  type LogEntryInput,
  type LogsClient,
} from "./logs";

const entry = (seq: number, over: Partial<LogEntryInput> = {}): LogEntryInput => ({
  type: "line",
  source: { kind: "service", name: "api" },
  key: "service:api",
  run: 1,
  seq,
  ts: seq,
  stream: "stdout",
  text: `line ${seq}`,
  ...over,
});

const range = (from: number, to: number, over: Partial<LogEntryInput> = {}) =>
  Array.from({ length: to - from + 1 }, (_, i) => entry(from + i, over));

describe("LogBuffer", () => {
  it("appends and evicts the oldest at capacity", () => {
    const b = new LogBuffer(3);
    expect(b.append(range(1, 2)).evicted).toHaveLength(0);
    const { evicted } = b.append(range(3, 5));
    expect(evicted.map((e) => e.seq)).toEqual([1, 2]);
    expect(b.toArray().map((e) => e.seq)).toEqual([3, 4, 5]);
    expect(b.size).toBe(3);
  });

  it("keeps only the newest entries of an oversized batch", () => {
    const b = new LogBuffer(3);
    b.append(range(1, 10));
    expect(b.toArray().map((e) => e.seq)).toEqual([8, 9, 10]);
  });

  it("assigns strictly increasing ids across prepend and append", () => {
    const b = new LogBuffer(10);
    b.append(range(5, 6));
    b.prepend(range(3, 4));
    b.append(range(7, 7));
    b.prepend(range(1, 2));
    const all = b.toArray();
    expect(all.map((e) => e.seq)).toEqual([1, 2, 3, 4, 5, 6, 7]);
    for (let i = 1; i < all.length; i++) expect(all[i]!.id).toBeGreaterThan(all[i - 1]!.id);
  });

  it("prepends only into free space, keeping the newest older lines", () => {
    const b = new LogBuffer(4);
    b.append(range(10, 12));
    const added = b.prepend(range(5, 9));
    expect(added.map((e) => e.seq)).toEqual([9]);
    expect(b.toArray().map((e) => e.seq)).toEqual([9, 10, 11, 12]);
    expect(b.prepend(range(1, 2))).toHaveLength(0);
  });

  it("wraps around the ring correctly", () => {
    const b = new LogBuffer(3);
    for (let i = 1; i <= 7; i++) b.append([entry(i)]);
    expect(b.first()?.seq).toBe(5);
    expect(b.last()?.seq).toBe(7);
    expect(b.at(3)).toBeUndefined();
  });
});

describe("LogModel", () => {
  it("filters rows and keeps filtered rows in sync with eviction", () => {
    const m = new LogModel(5);
    m.append(range(1, 5, {}).map((e) => ({ ...e, text: e.seq % 2 ? "odd" : "even" })));
    m.setFilter({ text: compileQuery("odd", false).matcher, hiddenSources: new Set(), hiddenStreams: new Set() });
    expect(m.count).toBe(3);
    const change = m.append([{ ...entry(6), text: "even" }, { ...entry(7), text: "odd" }]);
    // seq 1 (odd, visible) and 2 (even, hidden) were evicted.
    expect(change).toMatchObject({ appended: 1, evicted: 1, prepended: 0 });
    expect(Array.from({ length: m.count }, (_, i) => m.at(i)!.seq)).toEqual([3, 5, 7]);
  });

  it("hides sources and streams but keeps dividers of visible sources", () => {
    const m = new LogModel(100);
    m.append([
      entry(1),
      entry(2, { stream: "stderr" }),
      { ...entry(0), type: "divider", text: "run 2 started", run: 2 },
      entry(1, { key: "service:db", source: { kind: "service", name: "db" } }),
    ]);
    m.setFilter({ text: null, hiddenSources: new Set(["service:db"]), hiddenStreams: new Set(["stderr"]) });
    expect(Array.from({ length: m.count }, (_, i) => m.at(i)!.type + m.at(i)!.seq)).toEqual(["line1", "divider0"]);
  });

  it("tracks search hits incrementally and across eviction", () => {
    const m = new LogModel(4);
    m.setSearch(compileQuery("\\d*[05]$", true).matcher);
    m.append(range(1, 6));
    expect(m.hitCount).toBe(1); // 5 (buffer holds 3..6)
    const change = m.append(range(7, 10));
    expect(m.hitCount).toBe(1); // 10
    expect(change.hitsEvicted).toBe(1);
    const id = m.hitId(0)!;
    expect(m.at(m.indexOfId(id))!.seq).toBe(10);
  });

  it("strips ANSI before matching", () => {
    const m = new LogModel(10);
    m.append([{ ...entry(1), text: "\x1b[31mERR\x1b[0mOR here" }]);
    m.setSearch(compileQuery("error", false).matcher);
    expect(m.hitCount).toBe(1);
  });

  it("finds the next hit from an id, wrapping", () => {
    const m = new LogModel(10);
    m.append(range(1, 6).map((e) => ({ ...e, text: e.seq % 3 === 0 ? "hit" : "miss" })));
    m.setSearch(compileQuery("hit", false).matcher);
    const ids = [m.hitId(0)!, m.hitId(1)!];
    expect(m.hitIndexFrom(ids[0]! + 1)).toBe(1);
    expect(m.hitIndexFrom(ids[1]! + 1)).toBe(0);
  });

  it("reports invalid regexes", () => {
    expect(compileQuery("(", true).error).toBeTruthy();
    expect(compileQuery("(", false).matcher?.("a(b")).toBe(true);
  });
});

describe("ordering helpers", () => {
  it("LastSeen drops duplicates and detects new runs", () => {
    const seen = new LastSeen();
    expect(seen.accept(entry(1))).toBe("first");
    expect(seen.accept(entry(2))).toBe("ok");
    expect(seen.accept(entry(2))).toBe("dup");
    expect(seen.accept(entry(1))).toBe("dup");
    expect(seen.accept(entry(1, { run: 2 }))).toBe("new-run");
    expect(seen.accept(entry(99, { run: 1 }))).toBe("dup");
    expect(seen.latestRun("service:api")).toBe(2);
  });

  it("mergeOlder keeps the newest lines before the boundary in time order", () => {
    const a = range(1, 5, {}).map((e) => ({ ...e, ts: e.seq * 10 }));
    const b = range(1, 5, { key: "service:db", source: { kind: "service", name: "db" } }).map((e) => ({
      ...e,
      ts: e.seq * 10 + 5,
    }));
    const merged = mergeOlder([a, b], 4, entry(0, { ts: 45 }));
    expect(merged.map((e) => `${e.source.name}:${e.ts}`)).toEqual(["db:25", "api:30", "db:35", "api:40"]);
  });
});

// ------------------------------------------------------------------ stream

const line = (seq: number, run = 1, name = "api", text = `l${seq}`) =>
  new LogLine({
    source: new LogSource({ kind: "service", name }),
    run: BigInt(run),
    seq: BigInt(seq),
    tsUnixNanos: BigInt(seq) * 1_000_000n,
    stream: "stdout",
    text,
  });

type Responder = (req: PartialMessage<LogsRequest>) => PartialMessage<LogsResponse>[] | Error;

function fakeClient(responder: Responder): LogsClient & { requests: PartialMessage<LogsRequest>[] } {
  const requests: PartialMessage<LogsRequest>[] = [];
  return {
    requests,
    logs(req) {
      requests.push(req);
      const out = responder(req);
      return (async function* () {
        if (out instanceof Error) throw out;
        for (const r of out) yield new LogsResponse(r);
      })();
    },
  };
}

const drain = () => new Promise((r) => setTimeout(r, 0));
const seqs = (s: LogStream) =>
  Array.from({ length: s.model.count }, (_, i) => {
    const e = s.model.at(i)!;
    return e.type === "divider" ? `|${e.text}|` : e.seq;
  });

describe("LogStream", () => {
  it("loads history, batches appends through the scheduler and finishes", async () => {
    let scheduled: (() => void) | null = null;
    const client = fakeClient(() => [{ lines: [line(1), line(2)], hasMoreBefore: true }, { lines: [line(3)] }]);
    const s = new LogStream(
      { project: "web", sources: [{ kind: "service", name: "api" }], runOffset: -1, follow: false },
      { client, schedule: (cb) => (scheduled = cb) },
    );
    s.start();
    await drain();
    expect(s.phase()).toBe("done");
    expect(s.hasMoreBefore()).toBe(true);
    expect(seqs(s)).toEqual([1, 2, 3]);
    expect(client.requests[0]).toMatchObject({ project: "web", runOffset: -1n, follow: false });
    expect(scheduled).not.toBeNull();
  });

  it("inserts a run divider for new_run and dedupes reconnect tails", async () => {
    let call = 0;
    const client = fakeClient(() => {
      call++;
      if (call === 1) return [{ lines: [line(1), line(2)] }, { lines: [line(1, 2)], newRun: [new LogSource({ kind: "service", name: "api" })] }];
      if (call === 2) return new Error("daemon went away");
      // Reconnect tail overlaps what we already have.
      return [{ lines: [line(1, 2), line(2, 2), line(3, 2)] }];
    });
    vi.useFakeTimers();
    const s = new LogStream(
      { project: "web", sources: [{ kind: "service", name: "api" }], runOffset: 0, follow: true },
      { client, schedule: (cb) => cb(), random: () => 0 },
    );
    s.start();
    await vi.advanceTimersByTimeAsync(1000);
    expect(s.phase()).toBe("reconnecting");
    s.dispose();
    vi.useRealTimers();
    expect(seqs(s)).toEqual([1, 2, "|run 2 started|", 1, 2, 3]);
    expect(client.requests.length).toBeGreaterThanOrEqual(3);
  });

  it("pages back a single source with before_seq and the right run offset", async () => {
    const client = fakeClient((req) => {
      if (req.beforeSeq) return [{ lines: [line(8), line(9)], hasMoreBefore: false }];
      return [{ lines: [line(10), line(11)], hasMoreBefore: true }];
    });
    const s = new LogStream(
      { project: "web", sources: [{ kind: "service", name: "api" }], runOffset: 0, follow: false },
      { client, schedule: (cb) => cb() },
    );
    s.start();
    await drain();
    await s.loadOlder();
    expect(seqs(s)).toEqual([8, 9, 10, 11]);
    expect(client.requests[1]).toMatchObject({ beforeSeq: 10n, follow: false, sources: [{ name: "api" }] });
    expect(s.hasMoreBefore()).toBe(false);
  });

  it("pages back a merged view per source and merges by time", async () => {
    const client = fakeClient((req) => {
      const name = req.sources?.[0]?.name;
      if (!req.beforeSeq) return [{ lines: [line(20, 1, "api"), line(21, 1, "db")], hasMoreBefore: true }];
      if (name === "api") return [{ lines: [line(15, 1, "api"), line(18, 1, "api")], hasMoreBefore: true }];
      if (name === "db") return [{ lines: [line(17, 1, "db"), line(19, 1, "db")], hasMoreBefore: false }];
      // A source with no buffered lines pages from the end (MAX seq).
      return [{ lines: [line(16, 1, "worker")] }];
    });
    const s = new LogStream(
      { project: "web", sources: [], runOffset: 0, follow: false },
      { client, schedule: (cb) => cb(), knownSources: () => [{ kind: "service", name: "worker" }] },
    );
    s.start();
    await drain();
    await s.loadOlder();
    const order = Array.from({ length: s.model.count }, (_, i) => `${s.model.at(i)!.source.name}${s.model.at(i)!.seq}`);
    expect(order).toEqual(["api15", "worker16", "db17", "api18", "db19", "api20", "db21"]);
    const worker = client.requests.find((r) => r.sources?.[0]?.name === "worker");
    expect(worker?.beforeSeq).toBe(18446744073709551615n);
    expect(client.requests.find((r) => r.sources?.[0]?.name === "db")?.beforeSeq).toBe(21n);
  });

  it("sourceKey is stable", () => {
    expect(sourceKey({ kind: "task", name: "x" })).toBe("task:x");
  });
});
