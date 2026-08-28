import { createSignal } from "solid-js";
import type { Timestamp } from "@bufbuild/protobuf";

/** A signal that ticks once per second, for uptime-style displays. */
export function createClock() {
  const [now, setNow] = createSignal(Date.now());
  const timer = setInterval(() => setNow(Date.now()), 1000);
  return { now, dispose: () => clearInterval(timer) };
}

export function formatBytes(bytes?: number | bigint): string {
  const num = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (!num || num <= 0) return "0 B";
  if (num < 1024) return `${num} B`;
  if (num < 1024 * 1024) return `${(num / 1024).toFixed(1)} KB`;
  return `${(num / (1024 * 1024)).toFixed(1)} MB`;
}

function parseTime(val: Timestamp | string | Date | undefined): Date | null {
  if (!val) return null;
  if (val instanceof Date) return isNaN(val.getTime()) ? null : val;
  if (typeof val === "object" && "toDate" in val && typeof (val as Timestamp).toDate === "function") {
    return (val as Timestamp).toDate();
  }
  if (typeof val === "string") {
    const d = new Date(val);
    return isNaN(d.getTime()) ? null : d;
  }
  return null;
}

export function formatUptime(startTime: Timestamp | string | Date | undefined, nowMs: number): string {
  const start = parseTime(startTime);
  if (!start) return "—";
  const diffSec = Math.max(0, Math.floor((nowMs - start.getTime()) / 1000));
  if (diffSec < 60) return `${diffSec}s`;
  const mins = Math.floor(diffSec / 60);
  if (mins < 60) return `${mins}m ${diffSec % 60}s`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours}h ${mins % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function formatAuthorTime(time: Timestamp | string | Date | undefined): string {
  const d = parseTime(time);
  if (!d) return String(time ?? "");
  return d.toLocaleString();
}

export function formatRelativeTime(time: Timestamp | string | Date | undefined): string {
  const d = parseTime(time);
  if (!d) return String(time ?? "");
  const diffSec = Math.floor((Date.now() - d.getTime()) / 1000);
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 2592000) return `${Math.floor(diffSec / 86400)}d ago`;
  return d.toLocaleDateString();
}
