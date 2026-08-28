import { untrack } from "solid-js";
import { createSignal } from "solid-js";
import type {
  ActionInfo,
  ActionState,
  DaemonInfo,
  GitBranch,
  GitCommit,
  GitDiffResult,
  GitLogPayload,
  GitStash,
  GitTag,
  PortBinding,
  ProjectInfo,
  ServiceState,
} from "~/lib/types";
import { onWS, sendWS } from "~/lib/ws";
import { pushRoute } from "~/lib/router";
import { sameArray } from "~/lib/utils";
import { pushToast, setShowAddProject } from "~/stores/app";
import { activeView, selectedProject } from "~/stores/nav";

// --- data signals -----------------------------------------------------------

const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
const [services, setServices] = createSignal<Record<string, ServiceState[]>>({});
const [actions, setActions] = createSignal<Record<string, ActionInfo[]>>({});
const [actionStates, setActionStates] = createSignal<Record<string, ActionState[]>>({});
const [ports, setPorts] = createSignal<Record<string, PortBinding[]>>({});
const [daemonInfo, setDaemonInfo] = createSignal<DaemonInfo | null>(null);

// git data, keyed per project
const [gitCommits, setGitCommits] = createSignal<Record<string, GitCommit[]>>({});
const [gitBranches, setGitBranches] = createSignal<Record<string, GitBranch[]>>({});
const [gitTags, setGitTags] = createSignal<Record<string, GitTag[]>>({});
const [gitStashes, setGitStashes] = createSignal<Record<string, GitStash[]>>({});
const [gitError, setGitError] = createSignal<Record<string, string>>({});
const [gitLoading, setGitLoading] = createSignal<Record<string, boolean>>({});
const [selectedCommitHash, setSelectedCommitHash] = createSignal<Record<string, string | null>>({});
const [selectedFilePath, setSelectedFilePath] = createSignal<Record<string, string | null>>({});
const [gitDiffs, setGitDiffs] = createSignal<Record<string, Record<string, GitDiffResult>>>({});
const [gitDiffLoading, setGitDiffLoading] = createSignal<Record<string, boolean>>({});
const [gitCommitLoading, setGitCommitLoading] = createSignal<Record<string, boolean>>({});
const [gitCommitError, setGitCommitError] = createSignal<Record<string, string>>({});

export {
  projects,
  services,
  actions,
  actionStates,
  ports,
  daemonInfo,
  gitCommits,
  gitBranches,
  gitTags,
  gitStashes,
  gitError,
  gitLoading,
  selectedCommitHash,
  selectedFilePath,
  gitDiffs,
  gitDiffLoading,
  gitCommitLoading,
  gitCommitError,
};

// --- equality ---------------------------------------------------------------

function sameProject(a: ProjectInfo, b: ProjectInfo): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.config_path === b.config_path &&
    a.running_services === b.running_services &&
    a.total_services === b.total_services
  );
}

function sameService(a: ServiceState, b: ServiceState): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exit_code === b.exit_code &&
    a.restarts === b.restarts &&
    a.started_at === b.started_at &&
    a.finished_at === b.finished_at &&
    a.has_health === b.has_health &&
    a.health === b.health
  );
}

function sameActionState(a: ActionState, b: ActionState): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exit_code === b.exit_code &&
    a.started_at === b.started_at &&
    a.finished_at === b.finished_at
  );
}

/** Drops cache entries for projects that no longer exist. */
export function pruneData(projectNames: Set<string>) {
  const keepMap = <T>(m: Record<string, T>): Record<string, T> | null => {
    let changed = false;
    const next: Record<string, T> = {};
    for (const [k, v] of Object.entries(m)) {
      if (projectNames.has(k)) next[k] = v;
      else changed = true;
    }
    return changed ? next : null;
  };
  setServices((m) => keepMap(m) ?? m);
  setActions((m) => keepMap(m) ?? m);
  setActionStates((m) => keepMap(m) ?? m);
}

/**
 * Live project-status refresh: service state pushes don't carry the project's
 * own status, so debounce a single list_projects fetch after bursts of events.
 */
let statusRefreshTimer: ReturnType<typeof setTimeout> | null = null;
export function scheduleProjectStatusRefresh() {
  if (statusRefreshTimer != null) return;
  statusRefreshTimer = setTimeout(() => {
    statusRefreshTimer = null;
    sendWS({ type: "list_projects" });
  }, 300);
}

// --- refresh ----------------------------------------------------------------

export function refreshAll() {
  sendWS({ type: "list_projects" });
  sendWS({ type: "daemon_status" });
  sendWS({ type: "list_ports" });
}

export function refreshServices(project: string) {
  sendWS({ type: "list_services", project });
}

export function refreshProjectDetail(project: string) {
  sendWS({ type: "list_services", project });
  sendWS({ type: "list_actions", project });
  sendWS({ type: "list_action_states", project });
  sendWS({ type: "list_ports", project });
}

export function refreshExpandedProjects(expandedNames: Set<string>, selected: string | null) {
  const needed = new Set(expandedNames);
  if (selected) needed.add(selected);
  for (const p of needed) {
    refreshProjectDetail(p);
  }
}

// --- lifecycle commands -----------------------------------------------------

export function startProject(project: string, configPath?: string) {
  const path = configPath || untrack(projects).find((p) => p.name === project)?.config_path;
  sendWS({ type: "start_project", project, config_path: path || "" });
  sendWS({ type: "list_projects" });
  refreshServices(project);
}

export function startProjectByPath(configPath: string, envFile?: string) {
  sendWS({ type: "start_project", config_path: configPath, env_file: envFile || "" });
  sendWS({ type: "list_projects" });
  setShowAddProject(false);
}

export function stopProject(project: string) {
  sendWS({ type: "stop_project", project });
  sendWS({ type: "list_projects" });
  refreshServices(project);
}

export function restartService(project: string, service: string) {
  sendWS({ type: "restart_service", project, service });
  refreshServices(project);
}

export function startService(project: string, service: string) {
  sendWS({ type: "start_service", project, service });
  refreshServices(project);
}

export function stopService(project: string, service: string) {
  sendWS({ type: "stop_service", project, service });
  refreshServices(project);
}

export function killService(project: string, service: string, signal = "SIGKILL") {
  sendWS({ type: "kill_service", project, service, signal });
  refreshServices(project);
}

export function runAction(project: string, actionName: string, args?: string[]) {
  const currentState = untrack(actionStates)[project]?.find((a) => a.name === actionName);
  if (currentState?.status === "running" || currentState?.status === "starting") {
    pushToast(`Action '${actionName}' is already running`, "info");
    return false;
  }
  sendWS({ type: "run_action", project, action: actionName, args });
  pushToast(`Started action '${actionName}'`, "info");
  return true;
}

export function fetchPorts(project?: string) {
  sendWS({ type: "list_ports", project });
}

export function fetchDaemonStatus() {
  sendWS({ type: "daemon_status" });
}

export function restartDaemon() {
  sendWS({ type: "restart_daemon" });
}

// --- git --------------------------------------------------------------------

/** Fetch the git commit log for a project. */
export function loadGitLog(name: string) {
  setGitLoading((m) => ({ ...m, [name]: true }));
  setGitError((m) => ({ ...m, [name]: "" }));
  sendWS({ type: "git_log", project: name });
}

/** Fetch the diff for one commit (or WORKDIR), optionally widening context. */
export function loadGitDiff(
  project: string,
  hash: string,
  contextLines?: number,
  forceRefresh?: boolean
) {
  const existing = untrack(gitDiffs)[project]?.[hash];
  if (existing && !forceRefresh && !contextLines) return;
  setGitDiffLoading((m) => ({ ...m, [project]: true }));
  sendWS({ type: "git_diff", project, hash, context_lines: contextLines });
}

/** Stage or unstage a file (or everything with stageAll) in the worktree. */
export function stageGitFile(project: string, path: string, unstage?: boolean, stageAll?: boolean) {
  sendWS({ type: "git_stage", project, path, unstage, stage_all: stageAll });
}

/** Stage uncommitted changes and create a commit. */
export function commitGitChanges(project: string, message: string) {
  if (!message.trim()) return;
  setGitCommitLoading((m) => ({ ...m, [project]: true }));
  setGitCommitError((m) => ({ ...m, [project]: "" }));
  sendWS({ type: "git_commit", project, message: message.trim() });
}

export function pushGit(project: string) {
  sendWS({ type: "git_push", project });
}
export function pullGit(project: string) {
  sendWS({ type: "git_pull", project });
}
export function fetchGit(project: string) {
  sendWS({ type: "git_fetch", project });
}

function clearWorkdirCache(project: string) {
  setGitDiffs((m) => {
    const copy = { ...(m[project] ?? {}) };
    delete copy["WORKDIR"];
    return { ...m, [project]: copy };
  });
}

/** Select a commit in the git view and fetch its diff. */
export function selectCommit(project: string, hash: string | null, opts?: { skipPush?: boolean }) {
  setSelectedCommitHash((m) => ({ ...m, [project]: hash }));
  setSelectedFilePath((m) => ({ ...m, [project]: null }));
  if (hash) {
    loadGitDiff(project, hash);
  }
  if (!opts?.skipPush) {
    pushRoute({ project, service: null, action: null, view: "git", commit: hash });
  }
}

/** Select which file of the selected diff to show (null = all). */
export function selectDiffFile(project: string, path: string | null) {
  setSelectedFilePath((m) => ({ ...m, [project]: path }));
}

/** Deselect the current commit of a project (e.g. when leaving its diff URL). */
export function clearSelectedCommit(project: string) {
  setSelectedCommitHash((m) => ({ ...m, [project]: null }));
  setSelectedFilePath((m) => ({ ...m, [project]: null }));
}

// --- WS wiring --------------------------------------------------------------

/** Late-bound nav hooks; assigned by stores/nav.ts during start(). */
const navHooks: { selectCommitRoute: (project: string, hash: string | null) => void } = {
  selectCommitRoute: () => {},
};

export function bindNavHooks(hooks: Partial<typeof navHooks>) {
  Object.assign(navHooks, hooks);
}

function requireNav() {
  return navHooks;
}

/** Registers every server-response handler. Called once from start(). */
export function initDataHandlers() {
  onWS("daemon_status", (resp) => {
    if (resp.data) setDaemonInfo(resp.data as DaemonInfo);
  });

  onWS("projects", (resp) => {
    const next = resp.data as ProjectInfo[];
    setProjects((prev) => (sameArray(prev, next, sameProject) ? prev : next));
    pruneData(new Set(next.map((p) => p.name)));
  });

  onWS("services", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = resp.data as ServiceState[];
    setServices((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, sameService)) return m;
      return { ...m, [project]: next };
    });
  });

  onWS("actions", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = (resp.data ?? []) as ActionInfo[];
    setActions((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, (a, b) => a.name === b.name && a.command === b.command)) return m;
      return { ...m, [project]: next };
    });
  });

  onWS("action_states", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    const next = (resp.data ?? []) as ActionState[];
    setActionStates((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, sameActionState)) return m;
      return { ...m, [project]: next };
    });
  });

  onWS("ports", (resp) => {
    const list = (resp.data ?? []) as PortBinding[];
    const project = resp.project ?? "";
    if (project === "") {
      // Global snapshot: rebuild the whole map so per-project badges, sidebar
      // chips, and the dialog's all-projects view stay in sync from one fetch.
      setPorts(() => {
        const next: Record<string, PortBinding[]> = { "": list };
        for (const b of list) {
          const bucket = next[b.project];
          if (bucket) {
            bucket.push(b);
          } else {
            next[b.project] = [b];
          }
        }
        return next;
      });
      return;
    }
    setPorts((m) => ({ ...m, [project]: list }));
  });

  onWS("state_changed", (resp) => {
    const next = resp.data as ServiceState;
    const project = resp.project;
    if (!project) return;
    let changed = false;
    setServices((m) => {
      const list = m[project];
      if (!list) return m;
      const updated = list.map((s) => (s.name === next.name ? next : s));
      if (sameArray(list, updated, sameService)) return m;
      changed = true;
      return { ...m, [project]: updated };
    });
    if (changed) {
      scheduleProjectStatusRefresh();
    }
  });

  onWS("action_state_changed", (resp) => {
    const next = resp.data as ActionState;
    const project = resp.project;
    if (!project) return;
    setActionStates((m) => {
      const list = m[project];
      const updated = list ? list.map((a) => (a.name === next.name ? next : a)) : [next];
      if (list && sameArray(list, updated, sameActionState)) return m;
      return { ...m, [project]: updated };
    });
  });

  onWS("action_done", (resp) => {
    if (resp.ok === false || resp.error) {
      pushToast(resp.error || `Action '${resp.action}' failed`, "error");
    } else {
      pushToast(`Action '${resp.action}' finished (exit code ${resp.exit_code ?? 0})`, "success");
    }
  });

  onWS("error", (resp) => {
    if (!resp.error) return;
    // Poll races: list_services for a project removed between ticks.
    if (/project .+ is not running/.test(resp.error)) return;
    pushToast(resp.error);
  });

  onWS("result", (resp) => {
    if (resp.ok === false || resp.error) {
      pushToast(resp.error || "action failed");
    }
  });

  onWS("git_commits", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) {
      setGitError((m) => ({ ...m, [project]: resp.error ?? "Failed to load git log" }));
      return;
    }
    const payload = resp.data as GitLogPayload;
    if (!payload || Array.isArray(payload)) return;
    setGitCommits((m) => ({ ...m, [project]: payload.commits ?? [] }));
    setGitBranches((m) => ({ ...m, [project]: payload.branches ?? [] }));
    setGitTags((m) => ({ ...m, [project]: payload.tags ?? [] }));
    setGitStashes((m) => ({ ...m, [project]: payload.stashes ?? [] }));
  });

  onWS("git_diff", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitDiffLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) return; // surfaced through git_commit_error/toasts only
    const diffResult = resp.data as GitDiffResult;
    if (diffResult?.commit) {
      const hash = diffResult.commit.hash;
      setGitDiffs((m) => ({
        ...m,
        [project]: { ...(m[project] ?? {}), [hash]: diffResult },
      }));
    }
  });

  onWS("git_commit_result", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    setGitCommitLoading((m) => ({ ...m, [project]: false }));
    if (resp.ok === false) {
      setGitCommitError((m) => ({ ...m, [project]: resp.error ?? "Failed to commit changes" }));
      return;
    }
    setGitCommitError((m) => ({ ...m, [project]: "" }));
    clearWorkdirCache(project);
    setSelectedCommitHash((m) => ({ ...m, [project]: null }));
    loadGitLog(project);
    pushToast("Committed changes", "success");
  });

  onWS("git_stage_result", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    clearWorkdirCache(project);
    loadGitDiff(project, "WORKDIR", undefined, true);
  });

  onWS("git_remote_result", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    if (resp.ok === false || resp.error) {
      pushToast(resp.error || "Git operation failed", "error");
      return;
    }
    clearWorkdirCache(project);
    loadGitLog(project);
    const selCommit = untrack(selectedCommitHash)[project];
    if (selCommit) {
      loadGitDiff(project, selCommit, undefined, true);
    }
    if (resp.line) {
      pushToast(resp.line.trim(), "info");
    }
  });

  onWS("git_changed", (resp) => {
    if (!resp.project) return;
    const project = resp.project;
    clearWorkdirCache(project);
    if (untrack(activeView) === "git" || untrack(selectedProject) === project) {
      loadGitLog(project);
      const selCommit = untrack(selectedCommitHash)[project];
      if (selCommit) {
        loadGitDiff(project, selCommit, undefined, true);
      }
    }
  });
}
