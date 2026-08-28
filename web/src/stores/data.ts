import { untrack } from "solid-js";
import { createSignal } from "solid-js";
import type {
  ActionInfo,
  ActionState,
  DaemonInfo,
  GitBranch,
  GitCommit,
  GitDiffResult,
  GitStash,
  GitTag,
  PortBinding,
  ProjectInfo,
  ServiceState,
} from "~/lib/types";
import { rpcClient } from "~/lib/rpc";
import { onDaemonEvent } from "~/lib/events";
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
    a.configPath === b.configPath &&
    a.runningServices === b.runningServices &&
    a.totalServices === b.totalServices
  );
}

function sameService(a: ServiceState, b: ServiceState): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exitCode === b.exitCode &&
    a.restarts === b.restarts &&
    a.hasHealth === b.hasHealth &&
    a.health === b.health
  );
}

function sameActionState(a: ActionState, b: ActionState): boolean {
  return (
    a.name === b.name &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exitCode === b.exitCode
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
    fetchProjects();
  }, 300);
}

// --- refresh / fetch --------------------------------------------------------

export async function fetchProjects() {
  try {
    const res = await rpcClient.listProjects({});
    const next = res.projects;
    setProjects((prev) => (sameArray(prev, next, sameProject) ? prev : next));
    pruneData(new Set(next.map((p) => p.name)));
  } catch (err: any) {
    pushToast(err.message || "Failed to fetch projects", "error");
  }
}

export async function fetchServices(project: string) {
  try {
    const res = await rpcClient.listServices({ project });
    const next = res.states;
    setServices((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, sameService)) return m;
      return { ...m, [project]: next };
    });
  } catch (err: any) {
    if (!/not running/i.test(err.message)) {
      pushToast(err.message || `Failed to fetch services for ${project}`, "error");
    }
  }
}

export async function fetchActions(project: string) {
  try {
    const res = await rpcClient.listActions({ project });
    const next = res.actions;
    setActions((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, (a, b) => a.name === b.name && a.command === b.command)) return m;
      return { ...m, [project]: next };
    });
  } catch (err: any) {
    // ignore if project not running
  }
}

export async function fetchActionStates(project: string) {
  try {
    const res = await rpcClient.listActionStates({ project });
    const next = res.states;
    setActionStates((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, next, sameActionState)) return m;
      return { ...m, [project]: next };
    });
  } catch (err: any) {
    // ignore if project not running
  }
}

export async function fetchPorts(project?: string) {
  try {
    const res = await rpcClient.ports({ project: project || "" });
    const list = res.ports;
    if (!project) {
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
  } catch (err: any) {
    // ignore
  }
}

export async function fetchDaemonStatus() {
  try {
    const res = await rpcClient.daemonStatus({});
    if (res.info) setDaemonInfo(res.info);
  } catch (err: any) {
    // ignore
  }
}

export function refreshAll() {
  fetchProjects();
  fetchDaemonStatus();
  fetchPorts();
}

export function refreshServices(project: string) {
  fetchServices(project);
}

export function refreshProjectDetail(project: string) {
  fetchServices(project);
  fetchActions(project);
  fetchActionStates(project);
  fetchPorts(project);
}

export function refreshExpandedProjects(expandedNames: Set<string>, selected: string | null) {
  const needed = new Set(expandedNames);
  if (selected) needed.add(selected);
  for (const p of needed) {
    refreshProjectDetail(p);
  }
}

// --- lifecycle commands -----------------------------------------------------

export async function startProject(project: string, configPath?: string) {
  const path = configPath || untrack(projects).find((p) => p.name === project)?.configPath;
  try {
    await rpcClient.startProject({
      configPath: path || "",
      build: false,
      removeOrphans: true,
    });
    fetchProjects();
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to start project ${project}`, "error");
  }
}

export async function startProjectByPath(configPath: string, envFile?: string) {
  try {
    await rpcClient.startProject({
      configPath,
      envFile: envFile || "",
      build: false,
      removeOrphans: true,
    });
    fetchProjects();
    setShowAddProject(false);
  } catch (err: any) {
    pushToast(err.message || `Failed to start project at ${configPath}`, "error");
  }
}

export async function stopProject(project: string) {
  try {
    await rpcClient.stopProject({ project });
    fetchProjects();
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to stop project ${project}`, "error");
  }
}

export async function restartService(project: string, service: string) {
  try {
    await rpcClient.restart({ project, service });
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to restart service ${service}`, "error");
  }
}

export async function startService(project: string, service: string) {
  try {
    await rpcClient.startService({ project, service });
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to start service ${service}`, "error");
  }
}

export async function stopService(project: string, service: string) {
  try {
    await rpcClient.stopService({ project, service });
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to stop service ${service}`, "error");
  }
}

export async function killService(project: string, service: string, signal = "SIGKILL") {
  try {
    await rpcClient.killService({ project, service, signal });
    refreshServices(project);
  } catch (err: any) {
    pushToast(err.message || `Failed to kill service ${service}`, "error");
  }
}

export function runAction(project: string, actionName: string, args?: string[]) {
  const currentState = untrack(actionStates)[project]?.find((a) => a.name === actionName);
  if (currentState?.status === "running" || currentState?.status === "starting") {
    pushToast(`Action '${actionName}' is already running`, "info");
    return false;
  }
  pushToast(`Started action '${actionName}'`, "info");

  (async () => {
    try {
      const stream = rpcClient.runAction({
        project,
        action: actionName,
        args: args || [],
      });
      let exitCode = 0;
      for await (const chunk of stream) {
        if (chunk.exitCode !== undefined) {
          exitCode = chunk.exitCode;
        }
      }
      pushToast(`Action '${actionName}' finished (exit code ${exitCode})`, "success");
    } catch (err: any) {
      pushToast(err.message || `Action '${actionName}' failed`, "error");
    }
  })();

  return true;
}

export async function restartDaemon() {
  try {
    await rpcClient.restartDaemon({});
    pushToast("Daemon restarting...", "info");
  } catch (err: any) {
    pushToast(err.message || "Failed to restart daemon", "error");
  }
}

// --- git --------------------------------------------------------------------

/** Fetch the git commit log for a project. */
export async function loadGitLog(name: string) {
  setGitLoading((m) => ({ ...m, [name]: true }));
  setGitError((m) => ({ ...m, [name]: "" }));
  try {
    const res = await rpcClient.gitLog({ project: name });
    setGitCommits((m) => ({ ...m, [name]: res.commits ?? [] }));
    setGitBranches((m) => ({ ...m, [name]: res.branches ?? [] }));
    setGitTags((m) => ({ ...m, [name]: res.tags ?? [] }));
    setGitStashes((m) => ({ ...m, [name]: res.stashes ?? [] }));
  } catch (err: any) {
    setGitError((m) => ({ ...m, [name]: err.message || "Failed to load git log" }));
  } finally {
    setGitLoading((m) => ({ ...m, [name]: false }));
  }
}

/** Fetch the diff for one commit (or WORKDIR), optionally widening context. */
export async function loadGitDiff(
  project: string,
  hash: string,
  contextLines?: number,
  forceRefresh?: boolean
) {
  const existing = untrack(gitDiffs)[project]?.[hash];
  if (existing && !forceRefresh && !contextLines) return;
  setGitDiffLoading((m) => ({ ...m, [project]: true }));
  try {
    const res = await rpcClient.gitDiff({
      project,
      hash,
      contextLines: contextLines || 0,
    });
    if (res.result) {
      setGitDiffs((m) => ({
        ...m,
        [project]: { ...(m[project] ?? {}), [hash]: res.result! },
      }));
    }
  } catch (err: any) {
    pushToast(err.message || "Failed to load diff", "error");
  } finally {
    setGitDiffLoading((m) => ({ ...m, [project]: false }));
  }
}

/** Stage or unstage a file (or everything with stageAll) in the worktree. */
export async function stageGitFile(project: string, path: string, unstage?: boolean, stageAll?: boolean) {
  try {
    await rpcClient.gitStage({
      project,
      path,
      unstage: unstage || false,
      stageAll: stageAll || false,
    });
    clearWorkdirCache(project);
    loadGitDiff(project, "WORKDIR", undefined, true);
  } catch (err: any) {
    pushToast(err.message || "Failed to stage git file", "error");
  }
}

/** Stage uncommitted changes and create a commit. */
export async function commitGitChanges(project: string, message: string) {
  if (!message.trim()) return;
  setGitCommitLoading((m) => ({ ...m, [project]: true }));
  setGitCommitError((m) => ({ ...m, [project]: "" }));
  try {
    await rpcClient.gitCommit({
      project,
      message: message.trim(),
    });
    clearWorkdirCache(project);
    setSelectedCommitHash((m) => ({ ...m, [project]: null }));
    loadGitLog(project);
    pushToast("Committed changes", "success");
  } catch (err: any) {
    setGitCommitError((m) => ({ ...m, [project]: err.message || "Failed to commit changes" }));
  } finally {
    setGitCommitLoading((m) => ({ ...m, [project]: false }));
  }
}

export async function pushGit(project: string) {
  try {
    const res = await rpcClient.gitPush({ project });
    clearWorkdirCache(project);
    loadGitLog(project);
    if (res.output) pushToast(res.output.trim(), "info");
  } catch (err: any) {
    pushToast(err.message || "Git push failed", "error");
  }
}

export async function pullGit(project: string) {
  try {
    const res = await rpcClient.gitPull({ project });
    clearWorkdirCache(project);
    loadGitLog(project);
    if (res.output) pushToast(res.output.trim(), "info");
  } catch (err: any) {
    pushToast(err.message || "Git pull failed", "error");
  }
}

export async function fetchGit(project: string) {
  try {
    const res = await rpcClient.gitFetch({ project });
    clearWorkdirCache(project);
    loadGitLog(project);
    if (res.output) pushToast(res.output.trim(), "info");
  } catch (err: any) {
    pushToast(err.message || "Git fetch failed", "error");
  }
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

// --- Event Subscription Wiring ----------------------------------------------

/** Late-bound nav hooks; assigned by stores/nav.ts during start(). */
const navHooks: { selectCommitRoute: (project: string, hash: string | null) => void } = {
  selectCommitRoute: () => {},
};

export function bindNavHooks(hooks: Partial<typeof navHooks>) {
  Object.assign(navHooks, hooks);
}

/** Registers real-time daemon event handlers. Called once from start(). */
export function initDataHandlers() {
  onDaemonEvent((event) => {
    if (event.event.case === "serviceStateChanged") {
      const { project, state } = event.event.value;
      if (!project || !state) return;
      let changed = false;
      setServices((m) => {
        const list = m[project];
        if (!list) return m;
        const updated = list.map((s) => (s.name === state.name ? state : s));
        if (sameArray(list, updated, sameService)) return m;
        changed = true;
        return { ...m, [project]: updated };
      });
      if (changed) {
        scheduleProjectStatusRefresh();
      }
    } else if (event.event.case === "actionStateChanged") {
      const { project, state } = event.event.value;
      if (!project || !state) return;
      setActionStates((m) => {
        const list = m[project];
        const updated = list ? list.map((a) => (a.name === state.name ? state : a)) : [state];
        if (list && sameArray(list, updated, sameActionState)) return m;
        return { ...m, [project]: updated };
      });
    } else if (event.event.case === "gitChanged") {
      const { project } = event.event.value;
      if (!project) return;
      clearWorkdirCache(project);
      if (untrack(activeView) === "git" && untrack(selectedProject) === project) {
        loadGitLog(project);
        const selCommit = untrack(selectedCommitHash)[project];
        if (selCommit) {
          loadGitDiff(project, selCommit, undefined, true);
        }
      }
    }
  });
}
