const enc = encodeURIComponent;

export const paths = {
  home: () => "/",
  settings: () => "/settings",
  project: (project: string) => `/projects/${enc(project)}`,
  service: (project: string, service: string, tab?: string) =>
    `/projects/${enc(project)}/services/${enc(service)}${tab ? `?tab=${enc(tab)}` : ""}`,
  task: (project: string, task: string) => `/projects/${enc(project)}/tasks/${enc(task)}`,
  git: (project: string) => `/projects/${enc(project)}/git`,
};

export type RouteTarget =
  | { kind: "app" }
  | { kind: "project"; project: string }
  | { kind: "service"; project: string; name: string }
  | { kind: "task"; project: string; name: string };

/** Maps a URL pathname to the entity the page is about (for shortcuts/palette). */
export function targetFromPath(pathname: string): RouteTarget {
  const parts = pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts[0] !== "projects" || !parts[1]) return { kind: "app" };
  const project = parts[1];
  if (parts[2] === "services" && parts[3]) return { kind: "service", project, name: parts[3] };
  if (parts[2] === "tasks" && parts[3]) return { kind: "task", project, name: parts[3] };
  return { kind: "project", project };
}
