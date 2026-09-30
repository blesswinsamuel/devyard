import { createRoot, createSignal } from "solid-js";

/** A shared 1 Hz clock for uptime/relative-time displays. */
export const now = createRoot(() => {
  const [value, setValue] = createSignal(Date.now());
  if (typeof window !== "undefined") setInterval(() => setValue(Date.now()), 1000);
  return value;
});

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 100 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

export function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)}%`;
}

/** Compact duration: 850ms, 12s, 4m 10s, 3h 5m, 2d 4h. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ${sec % 60}s`;
  const hours = Math.floor(min / 60);
  if (hours < 48) return `${hours}h ${min % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function formatRelative(ms: number, nowMs: number): string {
  if (!ms) return "—";
  const diff = Math.round((nowMs - ms) / 1000);
  if (diff < 5) return "just now";
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return new Date(ms).toLocaleDateString();
}

const pad2 = (n: number) => String(n).padStart(2, "0");

/** Local wall-clock time with milliseconds: 14:03:07.123 */
export function formatClock(ms: number): string {
  const d = new Date(ms);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, "0")}`;
}

export function formatDateTime(ms: number): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleString();
}


/** Strips the scheme for compact URL display. */
export function displayUrl(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "");
}
