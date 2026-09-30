import type {
  ProjectEntity,
  ServiceEntity,
  ServiceStatus,
  TaskEntity,
  TaskStatus,
} from "~/data/entities";

/** Semantic tone shared by dots, badges and row accents. */
export type Tone = "success" | "warning" | "danger" | "muted" | "info";

const TRANSITIONAL_SERVICE: ServiceStatus[] = ["waiting", "building", "starting", "stopping", "backoff"];

export function serviceTone(s: Pick<ServiceEntity, "status" | "health" | "exitCode">): Tone {
  switch (s.status) {
    case "running":
      if (s.health === "unhealthy") return "danger";
      if (s.health === "starting") return "warning";
      return "success";
    case "failed":
      return "danger";
    case "exited":
      return s.exitCode === 0 ? "muted" : "danger";
    case "backoff":
      return "danger";
    case "stopped":
      return "muted";
    default:
      return TRANSITIONAL_SERVICE.includes(s.status) ? "warning" : "muted";
  }
}

export function projectTone(p: Pick<ProjectEntity, "status">): Tone {
  switch (p.status) {
    case "running":
      return "success";
    case "degraded":
    case "error":
      return "danger";
    case "starting":
    case "stopping":
      return "warning";
    default:
      return "muted";
  }
}

export function taskTone(t: Pick<TaskEntity, "status" | "exitCode" | "run">): Tone {
  switch (t.status) {
    case "running":
      return "info";
    case "waiting":
    case "stopping":
      return "warning";
    case "failed":
      return "danger";
    case "exited":
      return t.exitCode === 0 ? "success" : "danger";
    default:
      return "muted";
  }
}

/** A tone is "busy" when the entity is in a transitional state (pulse the dot). */
export function isTransitional(status: string): boolean {
  return ["waiting", "building", "starting", "stopping", "backoff"].includes(status);
}

/** Services that need the user's attention (crash / crash-loop / unhealthy). */
export function isServiceFailing(s: Pick<ServiceEntity, "status" | "health" | "exitCode">): boolean {
  return (
    s.status === "failed" ||
    s.status === "backoff" ||
    (s.status === "exited" && s.exitCode !== 0) ||
    (s.status === "running" && s.health === "unhealthy")
  );
}

export const SERVICE_STARTABLE: ServiceStatus[] = ["stopped", "exited", "failed"];
export const SERVICE_STOPPABLE: ServiceStatus[] = ["waiting", "building", "starting", "running", "backoff"];
/** States with a live (or imminent) process that can be signalled. */
export const SERVICE_KILLABLE: ServiceStatus[] = ["starting", "running", "stopping", "backoff", "building"];
export const TASK_ACTIVE: TaskStatus[] = ["waiting", "running", "stopping"];

export function serviceLabel(s: Pick<ServiceEntity, "status" | "exitCode">): string {
  if (s.status === "exited") return s.exitCode === 0 ? "exited" : `exited ${s.exitCode}`;
  return s.status;
}

export function taskLabel(t: Pick<TaskEntity, "status" | "exitCode" | "run">): string {
  if (t.status === "idle") return t.run > 0 ? "idle" : "never run";
  if (t.status === "exited") return t.exitCode === 0 ? "succeeded" : `exited ${t.exitCode}`;
  return t.status;
}

export const toneText: Record<Tone, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
  muted: "text-muted-foreground",
  info: "text-info",
};

export const toneBg: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-destructive",
  muted: "bg-muted-foreground/50",
  info: "bg-info",
};

export const toneBadge = {
  success: "success-light",
  warning: "warning-light",
  danger: "destructive-light",
  muted: "secondary",
  info: "info-light",
} as const satisfies Record<Tone, string>;
