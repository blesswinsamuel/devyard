export interface RouteState {
  project: string | null;
  service: string | null;
  action: string | null;
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
      return { project, service: parts[3], action: null };
    }
    if (parts[2] === "actions" && parts[3]) {
      return { project, service: null, action: parts[3] };
    }
    return { project, service: null, action: null };
  }
  return { project: null, service: null, action: null };
}

export function buildRoute(state: RouteState): string {
  if (!state.project) return "/";
  const p = encodeURIComponent(state.project);
  if (state.service) {
    return `/projects/${p}/services/${encodeURIComponent(state.service)}`;
  }
  if (state.action) {
    return `/projects/${p}/actions/${encodeURIComponent(state.action)}`;
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
