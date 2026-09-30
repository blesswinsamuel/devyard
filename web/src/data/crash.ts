import { createEffect, createRoot, createSignal } from "solid-js";
import { toast } from "solid-sonner";
import { createPersistedSignal, isBoolean } from "~/lib/persistence";
import { paths } from "~/lib/paths";
import { isServiceFailing } from "~/lib/status";
import { entities, type ServiceEntity, type TaskEntity } from "./entities";

/**
 * Crash awareness: toasts (and optional browser notifications) for live
 * transitions into a failing state, plus a failing count in the tab title
 * and favicon.
 */

export const [notificationsEnabled, setNotificationsEnabled] = createPersistedSignal(
  "notifications",
  false,
  isBoolean,
);

const CRASH_TOAST_MS = 12_000;

/** Page-specific title prefix, set by pages. */
const [pageTitle, setPageTitle] = createSignal("");
export { setPageTitle };

/** Why a live service transition deserves a toast, or null. Entering a
 * failing state is news; so is moving between failing states (backoff →
 * failed, unhealthy → exited). Repeats of the same state are not. */
export function crashReason(prev: ServiceEntity | undefined, next: ServiceEntity): string | null {
  if (!isServiceFailing(next)) return null;
  if (prev && isServiceFailing(prev) && prev.status === next.status) return null;
  switch (next.status) {
    case "failed":
      return next.message || `failed (exit ${next.exitCode})`;
    case "exited":
      return `exited with code ${next.exitCode}`;
    case "backoff":
      return next.message || `crashed (exit ${next.exitCode}), restarting`;
    default:
      return next.healthDetail ? `unhealthy: ${next.healthDetail}` : "unhealthy";
  }
}

export function taskFailureReason(prev: TaskEntity | undefined, next: TaskEntity): string | null {
  if (prev?.status === next.status && prev.run === next.run) return null;
  if (next.status === "failed") return next.message || "failed";
  if (next.status === "exited" && next.exitCode !== 0) return `exited with code ${next.exitCode}`;
  return null;
}

function notify(title: string, body: string, tag: string) {
  if (!notificationsEnabled() || typeof Notification === "undefined") return;
  if (Notification.permission !== "granted" || !document.hidden) return;
  try {
    new Notification(title, { body, tag });
  } catch {
    // Some browsers only allow notifications from a service worker.
  }
}

function failingCount(): number {
  let n = 0;
  for (const s of Object.values(entities.state.services)) if (isServiceFailing(s)) n++;
  return n;
}

function faviconSvg(count: number): string {
  const badge =
    count > 0
      ? `<circle cx="24" cy="8" r="8" fill="#e11d48"/><text x="24" y="12" font-family="system-ui,sans-serif" font-size="11" font-weight="700" text-anchor="middle" fill="#fff">${count > 9 ? "9+" : count}</text>`
      : "";
  return `data:image/svg+xml,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect x="2" y="4" width="26" height="24" rx="6" fill="#4f46e5"/><path d="M9 12l5 4-5 4M16 21h7" stroke="#fff" stroke-width="2.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/>${badge}</svg>`,
  )}`;
}

/** Wires toasts, title and favicon. `navigate` opens a logs page. */
export function startCrashAwareness(navigate: (path: string) => void): () => void {
  const unsubscribe = entities.subscribe((e) => {
    if (e.kind === "service") {
      const reason = crashReason(e.prev, e.next);
      if (!reason) return;
      const { project, name } = e.next;
      toast.error(`${name} ${e.next.health === "unhealthy" && e.next.status === "running" ? "is unhealthy" : "crashed"}`, {
        id: `crash:${project}/${name}`,
        duration: CRASH_TOAST_MS,
        description: `${project} · ${reason}`,
        action: { label: "View logs", onClick: () => navigate(paths.service(project, name, "logs")) },
      });
      notify(`${project}/${name}`, reason, `crash:${project}/${name}`);
    } else if (e.kind === "task") {
      const reason = taskFailureReason(e.prev, e.next);
      if (!reason) return;
      const { project, name } = e.next;
      toast.error(`Task ${name} failed`, {
        id: `task:${project}/${name}`,
        duration: CRASH_TOAST_MS,
        description: `${project} · ${reason}`,
        action: { label: "View output", onClick: () => navigate(paths.task(project, name)) },
      });
      notify(`${project}: task ${name}`, reason, `task:${project}/${name}`);
    }
  });

  const dispose = createRoot((dispose) => {
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    const icon = link;
    createEffect(() => {
      const n = failingCount();
      const page = pageTitle();
      document.title = `${n ? `(${n} failing) ` : ""}${page ? `${page} · ` : ""}devyard`;
      icon.href = faviconSvg(n);
    });
    return dispose;
  });

  return () => {
    unsubscribe();
    dispose();
  };
}
