export interface RouteState {
  project: string | null;
  service: string | null;
  task: string | null;
  view?: "logs" | "git" | null;
  commit?: string | null;
}

export function parseRoute(pathname = window.location.pathname): RouteState {
  const parts = pathname.split("/").filter(Boolean).map((p) => {
    try {
      return decodeURIComponent(p);
    } catch {
      return p;
    }
  });

  if (parts[0] === "projects" && parts[1]) {
    const project = parts[1];
    if (parts[2] === "services" && parts[3]) {
      return { project, service: parts[3], task: null, view: "logs", commit: null };
    }
    if (parts[2] === "tasks" && parts[3]) {
      return { project, service: null, task: parts[3], view: "logs", commit: null };
    }
    if (parts[2] === "git") {
      if (parts[3] === "commits" && parts[4]) {
        return { project, service: null, task: null, view: "git", commit: parts[4] };
      }
      if (parts[3] && parts[3] !== "commits") {
        return { project, service: null, task: null, view: "git", commit: parts[3] };
      }
      return { project, service: null, task: null, view: "git", commit: null };
    }
    return { project, service: null, task: null, view: "logs", commit: null };
  }
  return { project: null, service: null, task: null, view: "logs", commit: null };
}

export function buildRoute(state: RouteState): string {
  if (!state.project) return "/";
  const p = encodeURIComponent(state.project);
  if (state.view === "git") {
    if (state.commit) {
      return `/projects/${p}/git/commits/${encodeURIComponent(state.commit)}`;
    }
    return `/projects/${p}/git`;
  }
  if (state.service) {
    return `/projects/${p}/services/${encodeURIComponent(state.service)}`;
  }
  if (state.task) {
    return `/projects/${p}/tasks/${encodeURIComponent(state.task)}`;
  }
  return `/projects/${p}`;
}

export function pushRoute(state: RouteState) {
  const target = buildRoute(state);
  if (window.location.pathname !== target) {
    window.history.pushState(null, "", target);
  }
}

export function replaceRoute(state: RouteState) {
  const target = buildRoute(state);
  if (window.location.pathname !== target) {
    window.history.replaceState(null, "", target);
  }
}

export function listenPopState(cb: (state: RouteState) => void): () => void {
  const handler = () => {
    cb(parseRoute());
  };
  window.addEventListener("popstate", handler);
  return () => window.removeEventListener("popstate", handler);
}

