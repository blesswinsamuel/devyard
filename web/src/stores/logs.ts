import { untrack } from "solid-js";
import { createSignal } from "solid-js";
import { rpcClient } from "~/lib/rpc";

export interface LogTab {
  /** Stable identity: `s:project/service` or `t:project/task`. */
  key: string;
  project: string;
  kind: "service" | "task";
  name: string;
}

const [tabs, setTabs] = createSignal<LogTab[]>([]);
/** The selected tab; may be null (e.g. after closing the active one). */
const [activeKey, setActiveKey] = createSignal<string | null>(null);
/**
 * Tabs showing the previous run instead of the live one. Non-live panes
 * never follow or auto-reset.
 */
const [previousKeys, setPreviousKeys] = createSignal<Set<string>>(new Set());

export { tabs, activeKey, previousKeys };

export function tabKey(project: string, kind: LogTab["kind"], name: string): string {
  return `${kind === "service" ? "s" : "t"}:${project}/${name}`;
}

export function isPreviousLogs(key: string): boolean {
  return previousKeys().has(key);
}

export function togglePreviousLogs(key: string) {
  setPreviousKeys((prev) => {
    const next = new Set(prev);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  });
}

/** Opens (or focuses) a log tab. Deduped by key. */
export function openLogTab(project: string, kind: LogTab["kind"], name: string) {
  const key = tabKey(project, kind, name);
  setActiveKey(key);
  if (untrack(tabs).some((t) => t.key === key)) return;
  setTabs((prev) => [...prev, { key, project, kind, name }]);
}

export function closeLogTab(key: string) {
  setTabs((prev) => {
    const idx = prev.findIndex((t) => t.key === key);
    if (idx === -1) return prev;
    // If we're closing the active tab, activate its neighbor.
    const wasActive = untrack(activeKey) === key;
    if (wasActive) {
      const neighbor = prev[idx + 1] ?? prev[idx - 1];
      setActiveKey(neighbor?.key ?? null);
    }
    return prev.filter((t) => t.key !== key);
  });
}

/** Drops tabs whose target no longer exists (project removed / service deleted). */
export function pruneLogTabs(predicate: (tab: LogTab) => boolean) {
  setTabs((prev) => {
    const kept = prev.filter(predicate);
    if (kept.length === prev.length) return prev;
    if (untrack(activeKey) && !kept.some((t) => t.key === untrack(activeKey))) {
      setActiveKey(null);
    }
    return kept;
  });
  setPreviousKeys((prev) => {
    const currentTabs = untrack(tabs);
    const validKeys = new Set(currentTabs.map((t) => t.key));
    let changed = false;
    const next = new Set<string>();
    for (const k of prev) {
      if (validKeys.has(k)) next.add(k);
      else changed = true;
    }
    return changed ? next : prev;
  });
}

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
): () => void {
  const controller = new AbortController();
  (async () => {
    try {
      const stream = rpcClient.logs(
        {
          project,
          service,
          follow: !prev,
          previous: prev,
          tail: 500,
        },
        { signal: controller.signal }
      );

      for await (const chunk of stream) {
        if (controller.signal.aborted) break;
        if (chunk.rotated) {
          onRotate?.();
        }
        if (chunk.content) {
          const contentLines = chunk.content.split("\n");
          if (contentLines.length > 0 && contentLines[contentLines.length - 1] === "") {
            contentLines.pop();
          }
          for (const line of contentLines) {
            onLine(line);
          }
        }
        for (const line of chunk.lines) {
          onLine(line);
        }
      }
    } catch {
      // Abort or stream close
    }
  })();

  return () => controller.abort();
}

export function subscribeTaskLogs(
  project: string,
  task: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
): () => void {
  const controller = new AbortController();
  (async () => {
    try {
      const stream = rpcClient.logs(
        {
          project,
          task,
          follow: !prev,
          previous: prev,
          tail: 500,
        },
        { signal: controller.signal }
      );

      for await (const chunk of stream) {
        if (controller.signal.aborted) break;
        if (chunk.rotated) {
          onRotate?.();
        }
        if (chunk.content) {
          const contentLines = chunk.content.split("\n");
          if (contentLines.length > 0 && contentLines[contentLines.length - 1] === "") {
            contentLines.pop();
          }
          for (const line of contentLines) {
            onLine(line);
          }
        }
        for (const line of chunk.lines) {
          onLine(line);
        }
      }
    } catch {
      // Abort or stream close
    }
  })();

  return () => controller.abort();
}
