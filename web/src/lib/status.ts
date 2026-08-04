import type { ServiceState } from "~/types";

export type StatusTone = "success" | "warning" | "destructive" | "muted";

export function statusTone(status: string): StatusTone {
  switch (status) {
    case "running":
      return "success";
    case "starting":
    case "stopping":
      return "warning";
    case "backoff":
      return "destructive";
    case "exited":
    case "stopped":
      return "muted";
    default:
      return "muted";
  }
}

export function statusDot(status: string): string {
  switch (statusTone(status)) {
    case "success":
      return "bg-success";
    case "warning":
      return "bg-warning";
    case "destructive":
      return "bg-destructive";
    default:
      return "bg-muted-foreground/60";
  }
}

export function statusLabel(status: string, exitCode: number): string {
  if (status === "exited" && exitCode !== 0) {
    return `exited(${exitCode})`;
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

export function healthLabel(hasHealth: boolean, health: string): string {
  if (!hasHealth) return "-";
  if (!health) return "n/a";
  return health;
}

export function serviceMeta(s: ServiceState): string {
  const parts: string[] = [];
  parts.push(s.pid > 0 ? `pid ${s.pid}` : "no pid");
  parts.push(`${s.restarts} restart${s.restarts === 1 ? "" : "s"}`);
  parts.push(`health ${healthLabel(s.has_health, s.health)}`);
  return parts.join("  ·  ");
}
