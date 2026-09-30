import { createEffect, createSignal, onCleanup, type Accessor } from "solid-js";
import { createStore, produce, reconcile, type SetStoreFunction } from "solid-js/store";
import { backoffDelay, sleep } from "~/lib/backoff";
import { api } from "./client";
import { isCanceled } from "./errors";

export const STATS_INTERVAL_MS = 2_000;
export const STATS_HISTORY = 60;

export interface StatSample {
  ts: number;
  cpu: number;
  rss: number;
  procs: number;
  pid: number;
}

export interface StatSeries {
  latest: StatSample;
  cpu: number[];
  rss: number[];
}

/** Keyed by "<kind>/<name>" (kind = service | task). */
export type ProjectStats = Record<string, StatSeries>;

interface Subscription {
  refs: number;
  abort: AbortController;
  stats: ProjectStats;
  set: SetStoreFunction<ProjectStats>;
  stopTimer?: ReturnType<typeof setTimeout>;
}

const subs = new Map<string, Subscription>();

export const statKey = (kind: string, name: string) => `${kind}/${name}`;

function push(arr: number[], v: number) {
  arr.push(v);
  if (arr.length > STATS_HISTORY) arr.splice(0, arr.length - STATS_HISTORY);
}

async function pump(project: string, sub: Subscription) {
  let attempt = 0;
  while (!sub.abort.signal.aborted) {
    try {
      for await (const res of api.stats(
        { project, intervalMs: BigInt(STATS_INTERVAL_MS) },
        { signal: sub.abort.signal },
      )) {
        attempt = 0;
        const ts = Number(res.tsUnixMs);
        const seen = new Set<string>();
        sub.set(
          produce((all) => {
            for (const st of res.stats) {
              const key = statKey(st.kind, st.name);
              seen.add(key);
              const sample: StatSample = {
                ts,
                cpu: st.cpuPercent,
                rss: Number(st.rssBytes),
                procs: st.procs,
                pid: st.pid,
              };
              const series = (all[key] ??= { latest: sample, cpu: [], rss: [] });
              series.latest = sample;
              push(series.cpu, sample.cpu);
              push(series.rss, sample.rss);
            }
            // Processes that went away keep their history but read as idle.
            for (const key of Object.keys(all)) {
              if (seen.has(key)) continue;
              const series = all[key]!;
              series.latest = { ...series.latest, ts, cpu: 0, rss: 0, procs: 0 };
              push(series.cpu, 0);
              push(series.rss, 0);
            }
          }),
        );
      }
    } catch (err) {
      if (sub.abort.signal.aborted || isCanceled(err)) return;
    }
    attempt++;
    await sleep(backoffDelay(attempt), sub.abort.signal);
  }
}

function acquire(project: string): Subscription {
  let sub = subs.get(project);
  if (sub) {
    clearTimeout(sub.stopTimer);
    sub.refs++;
    return sub;
  }
  const [stats, set] = createStore<ProjectStats>({});
  sub = { refs: 1, abort: new AbortController(), stats, set };
  subs.set(project, sub);
  void pump(project, sub);
  return sub;
}

function release(project: string) {
  const sub = subs.get(project);
  if (!sub) return;
  sub.refs--;
  if (sub.refs > 0) return;
  // Linger briefly so navigating between a project's pages keeps history.
  sub.stopTimer = setTimeout(() => {
    sub.abort.abort();
    sub.set(reconcile({}));
    subs.delete(project);
  }, 15_000);
}

/**
 * Subscribes to a project's Stats stream while the calling component is
 * mounted. Streams are shared and ref-counted across components.
 */
export function useProjectStats(project: Accessor<string | undefined>): Accessor<ProjectStats> {
  const [sub, setSub] = createSignal<Subscription>();
  createEffect(() => {
    const p = project();
    if (!p) return;
    setSub(acquire(p));
    onCleanup(() => {
      setSub(undefined);
      release(p);
    });
  });
  return () => sub()?.stats ?? EMPTY_STATS;
}

const EMPTY_STATS: ProjectStats = {};
