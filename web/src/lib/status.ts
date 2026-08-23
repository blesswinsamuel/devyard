import type { ServiceState } from "~/lib/types";

export type StatusTone = "success" | "warning" | "destructive" | "info" | "muted";

export function statusTone(status: string): StatusTone {
  switch (status) {
    case "running":
      return "success";
    case "starting":
    case "stopping":
    case "backoff":
      return "warning";
    case "exited":
      return statusExitedTone();
    default:
      return "muted";
  }
}

function statusExitedTone(): StatusTone {
  return "muted";
}

export function statusDot(status: string): string {
  switch (statusTone(status)) {
    case "success":
      return "bg-success shadow-[0_0_6px_var(--success)]";
    case "warning":
      return "bg-warning animate-pulse";
    case "destructive":
      return "bg-destructive";
    case "info":
      return "bg-info animate-pulse";
    default:
      return "bg-muted-foreground/50";
  }
}

export function statusLabel(status: string, exitCode: number): string {
  if (status === "exited" && exitCode !== 0) {
    return `exited(${exitCode})`;
  }
  if (status === "backoff") {
    return "restart backoff";
  }
  return status;
}

export function healthTone(hasHealth: boolean, health: string): StatusTone {
  if (!hasHealth) return "muted";
  switch (health) {
    case "healthy":
      return "success";
    case "unhealthy":
      return "destructive";
    case "starting":
    case "starting_healthy":
      return "warning";
    default:
      return "muted";
  }
}

export function healthDot(hasHealth: boolean, health: string): string {
  if (!hasHealth) return "";
  switch (healthTone(hasHealth, health)) {
    case "success":
      return "bg-success";
    case "warning":
      return "bg-warning animate-pulse";
    case "destructive":
      return "bg-destructive animate-pulse";
    default:
      return "bg-muted-foreground/50";
  }
}

export function serviceMeta(s: ServiceState): string {
  const parts: string[] = [];
  if (s.pid > 0) {
    parts.push(`pid ${s.pid}`);
  }
  if (s.restarts > 0) {
    parts.push(`${s.restarts} restart${s.restarts === 1 ? "" : "s"}`);
  }
  return parts.join("  ·  ");
}
