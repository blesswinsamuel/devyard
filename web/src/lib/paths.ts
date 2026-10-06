const enc = encodeURIComponent;

/** Query string for the git route's view options (file history, diff base). */
export interface GitQuery {
  /** File-history filter (repository-relative path). */
  path?: string;
  /** Diff base: compare this ref to the selected commit (base..commit). */
  base?: string;
}

function gitQuery(q: GitQuery): string {
  const params = new URLSearchParams();
  if (q.path) params.set("path", q.path);
  if (q.base) params.set("base", q.base);
  const s = params.toString();
  return s ? `?${s}` : "";
}

export const paths = {
  home: () => "/",
  settings: () => "/settings",
  project: (project: string) => `/projects/${enc(project)}`,
  service: (project: string, service: string, tab?: string) =>
    `/projects/${enc(project)}/services/${enc(service)}${tab ? `?tab=${enc(tab)}` : ""}`,
  task: (project: string, task: string) => `/projects/${enc(project)}/tasks/${enc(task)}`,
  git: (project: string, query: GitQuery = {}) => `/projects/${enc(project)}/git${gitQuery(query)}`,
  gitHistory: (project: string, path: string) => paths.git(project, { path }),
  gitCommit: (project: string, hash: string, query: GitQuery = {}) =>
    `/projects/${enc(project)}/git/commits/${enc(hash)}${gitQuery(query)}`,
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
