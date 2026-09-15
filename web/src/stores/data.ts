import { untrack } from "solid-js";
import { createSignal } from "solid-js";
import type {
  DaemonInfo,
  GitBranch,
  GitCommit,
  GitDiffResult,
  GitStash,
  GitStatus,
  GitTag,
  PortBinding,
  ProjectInfo,
  ServiceState,
  TaskState,
} from "~/lib/types";
import { rpcClient } from "~/lib/rpc";
import { onConnectionOpen, onDaemonEvent } from "~/lib/events";
import { pushRoute } from "~/lib/router";
import { sameArray } from "~/lib/utils";
import { pushToast, setShowAddProject } from "~/stores/app";
import { activeView, selectedProject } from "~/stores/nav";

// --- data signals -----------------------------------------------------------

const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
const [services, setServices] = createSignal<Record<string, ServiceState[]>>({});
const [tasks, setTasks] = createSignal<Record<string, TaskState[]>>({});
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
const [gitStatuses, setGitStatuses] = createSignal<Record<string, GitStatus>>({});
// project -> remote git operation currently in progress ("pull" | "fetch" | "push"), "" when idle
const [gitSync, setGitSync] = createSignal<Record<string, string>>({});

export {
  projects,
  services,
  tasks,
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
  gitStatuses,
  gitSync,
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

function sameTaskState(a: TaskState, b: TaskState): boolean {
  return (
    a.name === b.name &&
    a.command === b.command &&
    a.workingDir === b.workingDir &&
    a.tty === b.tty &&
    (a.dependsOn?.join(",") ?? "") === (b.dependsOn?.join(",") ?? "") &&
    a.status === b.status &&
    a.pid === b.pid &&
    a.exitCode === b.exitCode
  );
}

function sameGitCommit(a: GitCommit, b: GitCommit): boolean {
  if (a.hash === "WORKDIR" && b.hash === "WORKDIR") {
    return (
      a.short === b.short &&
      a.author === b.author &&
      a.email === b.email &&
      a.subject === b.subject &&
      a.head === b.head &&
      (a.parents?.join(",") ?? "") === (b.parents?.join(",") ?? "") &&
      (a.refs?.map((r) => `${r.name}:${r.type}:${r.isActive}`).join(",") ?? "") ===
        (b.refs?.map((r) => `${r.name}:${r.type}:${r.isActive}`).join(",") ?? "")
    );
  }
  return (
    a.hash === b.hash &&
    a.short === b.short &&
    a.author === b.author &&
    a.email === b.email &&
    a.subject === b.subject &&
    a.head === b.head &&
    a.time?.seconds === b.time?.seconds &&
    a.time?.nanos === b.time?.nanos &&
    (a.parents?.join(",") ?? "") === (b.parents?.join(",") ?? "") &&
    (a.refs?.map((r) => `${r.name}:${r.type}:${r.isActive}`).join(",") ?? "") ===
      (b.refs?.map((r) => `${r.name}:${r.type}:${r.isActive}`).join(",") ?? "")
  );
}

function sameGitBranch(a: GitBranch, b: GitBranch): boolean {
  return (
    a.name === b.name &&
    a.hash === b.hash &&
    a.isActive === b.isActive &&
    a.isRemote === b.isRemote &&
    a.upstream === b.upstream &&
    a.ahead === b.ahead &&
    a.behind === b.behind
  );
}

function sameGitTag(a: GitTag, b: GitTag): boolean {
  return a.name === b.name && a.hash === b.hash;
}

function sameGitStash(a: GitStash, b: GitStash): boolean {
  return a.index === b.index && a.name === b.name && a.hash === b.hash;
}

function sameGitStatus(a?: GitStatus, b?: GitStatus): boolean {
  if (!a || !b) return a === b;
  return (
    a.project === b.project &&
    a.branch === b.branch &&
    a.upstream === b.upstream &&
    a.ahead === b.ahead &&
    a.behind === b.behind &&
    a.staged === b.staged &&
    a.dirty === b.dirty &&
    a.untracked === b.untracked &&
    a.conflicts === b.conflicts &&
    a.isClean === b.isClean &&
    a.isRepo === b.isRepo &&
    a.headHash === b.headHash
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
  setTasks((m) => keepMap(m) ?? m);
  setGitStatuses((m) => keepMap(m) ?? m);
  setGitSync((m) => keepMap(m) ?? m);
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
    fetchServices();
    fetchTasks();
    fetchPorts();
  }, 300);
}

export async function fetchProjects() {
  try {
    const res = await rpcClient.listProjects({});
    const next = res.projects;
    setProjects((prev) => (sameArray(prev, next, sameProject) ? prev : next));
    pruneData(new Set(next.map((p) => p.name)));
    for (const p of next) {
      fetchGitStatus(p.name);
    }
  } catch (err: any) {
    pushToast(err.message || "Failed to fetch projects", "error");
  }
}

export async function fetchServices(project?: string) {
  try {
    const res = await rpcClient.listServices({ project: project || "" });
    const list = res.states;
    if (!project) {
      setServices((prev) => {
        const next: Record<string, ServiceState[]> = {};
        for (const s of list) {
          const p = s.project || "";
          if (!next[p]) next[p] = [];
          next[p].push(s);
        }
        let changed = false;
        const prevKeys = Object.keys(prev);
        const nextKeys = Object.keys(next);
        if (prevKeys.length !== nextKeys.length) {
          changed = true;
        } else {
          for (const k of nextKeys) {
            if (!prev[k] || !sameArray(prev[k], next[k], sameService)) {
              changed = true;
              break;
            }
          }
        }
        return changed ? next : prev;
      });
      return;
    }
    setServices((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, list, sameService)) return m;
      return { ...m, [project]: list };
    });
  } catch (err: any) {
    if (!/not running/i.test(err.message)) {
      pushToast(err.message || (project ? `Failed to fetch services for ${project}` : "Failed to fetch services"), "error");
    }
  }
}

export async function fetchTasks(project?: string) {
  try {
    const res = await rpcClient.listTasks({ project: project || "" });
    const list = res.tasks;
    if (!project) {
      setTasks((prev) => {
        const next: Record<string, TaskState[]> = {};
        for (const a of list) {
          const p = a.project || "";
          if (!next[p]) next[p] = [];
          next[p].push(a);
        }
        let changed = false;
        const prevKeys = Object.keys(prev);
        const nextKeys = Object.keys(next);
        if (prevKeys.length !== nextKeys.length) {
          changed = true;
        } else {
          for (const k of nextKeys) {
            if (!prev[k] || !sameArray(prev[k], next[k], sameTaskState)) {
              changed = true;
              break;
            }
          }
        }
        return changed ? next : prev;
      });
      return;
    }
    setTasks((m) => {
      const prev = m[project];
      if (prev && sameArray(prev, list, sameTaskState)) return m;
      return { ...m, [project]: list };
    });
  } catch (err: any) {
    if (!/not running/i.test(err.message)) {
      pushToast(err.message || (project ? `Failed to fetch tasks for ${project}` : "Failed to fetch tasks"), "error");
    }
  }
}

export async function fetchPorts(project?: string) {
  try {
    const res = await rpcClient.listPorts({ project: project || "" });
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
    if (!/not running/i.test(err.message)) {
      pushToast(err.message || (project ? `Failed to fetch ports for ${project}` : "Failed to fetch ports"), "error");
    }
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
  fetchServices();
  fetchTasks();
  fetchPorts();
}

export function refreshServices(project: string) {
  fetchServices(project);
}

export function refreshTasks(project: string) {
  fetchTasks(project);
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
    refreshTasks(project);
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
    fetchServices();
    fetchTasks();
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
    refreshTasks(project);
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

export function runTask(project: string, taskName: string, args?: string[]) {
  const currentState = untrack(tasks)[project]?.find((a) => a.name === taskName);
  if (currentState?.status === "running" || currentState?.status === "starting") {
    pushToast(`Task '${taskName}' is already running`, "info");
    return false;
  }
  pushToast(`Started task '${taskName}'`, "info");

  (async () => {
    try {
      const stream = rpcClient.runTask({
        project,
        task: taskName,
        args: args || [],
      });
      let exitCode = 0;
      for await (const chunk of stream) {
        if (chunk.exitCode !== undefined) {
          exitCode = chunk.exitCode;
        }
      }
      pushToast(`Task '${taskName}' finished (exit code ${exitCode})`, "success");
    } catch (err: any) {
      pushToast(err.message || `Task '${taskName}' failed`, "error");
    }
  })();

  return true;
}

export async function stopTask(project: string, taskName: string) {
  try {
    await rpcClient.stopTask({ project, task: taskName });
    pushToast(`Stopped task '${taskName}'`, "info");
  } catch (err: any) {
    pushToast(err.message || `Failed to stop task ${taskName}`, "error");
  }
}

export async function restartDaemon(restartServices = false) {
  try {
    await rpcClient.restartDaemon({ restartServices });
    pushToast(restartServices ? "Daemon and services restarting..." : "Daemon restarting (services preserved)...", "info");
  } catch (err: any) {
    pushToast(err.message || "Failed to restart daemon", "error");
  }
}

// --- git --------------------------------------------------------------------

/** Fetch the git status summary for a project (branch, ahead/behind, staged/dirty/untracked). */
export async function fetchGitStatus(project: string) {
  try {
    const res = await rpcClient.gitStatus({ project });
    if (res.status) {
      setGitStatuses((m) => {
        if (sameGitStatus(m[project], res.status)) return m;
        return { ...m, [project]: res.status! };
      });
    }
  } catch (err: any) {
    // Silently ignore errors (e.g. non-git project or daemon offline)
  }
}

/** Fetch the git commit log for a project. */
export async function loadGitLog(name: string) {
  setGitLoading((m) => ({ ...m, [name]: true }));
  setGitError((m) => ({ ...m, [name]: "" }));
  try {
    const res = await rpcClient.gitLog({ project: name });
    const commits = res.commits ?? [];
    const branches = res.branches ?? [];
    const tags = res.tags ?? [];
    const stashes = res.stashes ?? [];
    setGitCommits((m) => (sameArray(m[name] ?? [], commits, sameGitCommit) ? m : { ...m, [name]: commits }));
    setGitBranches((m) => (sameArray(m[name] ?? [], branches, sameGitBranch) ? m : { ...m, [name]: branches }));
    setGitTags((m) => (sameArray(m[name] ?? [], tags, sameGitTag) ? m : { ...m, [name]: tags }));
    setGitStashes((m) => (sameArray(m[name] ?? [], stashes, sameGitStash) ? m : { ...m, [name]: stashes }));
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
    fetchGitStatus(project);
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
    fetchGitStatus(project);
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
    fetchGitStatus(project);
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
    fetchGitStatus(project);
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
    fetchGitStatus(project);
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
    pushRoute({ project, service: null, task: null, view: "git", commit: hash });
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
  onConnectionOpen(() => {
    refreshAll();
  });

  onDaemonEvent((event) => {
    if (event.event.case === "serviceStateChanged") {
      const { project, state } = event.event.value;
      if (!project || !state) return;
      let changed = false;
      setServices((m) => {
        const list = m[project] ?? [];
        const idx = list.findIndex((s) => s.name === state.name);
        const updated = idx >= 0 ? list.map((s, i) => (i === idx ? state : s)) : [...list, state];
        if (sameArray(list, updated, sameService)) return m;
        changed = true;
        return { ...m, [project]: updated };
      });
      if (changed) {
        scheduleProjectStatusRefresh();
      }
    } else if (event.event.case === "taskStateChanged") {
      const { project, state } = event.event.value;
      if (!project || !state) return;
      setTasks((m) => {
        const list = m[project] ?? [];
        const idx = list.findIndex((a) => a.name === state.name);
        const updated = idx >= 0 ? list.map((a, i) => (i === idx ? state : a)) : [...list, state];
        if (sameArray(list, updated, sameTaskState)) return m;
        return { ...m, [project]: updated };
      });
    } else if (event.event.case === "projectsChanged") {
      scheduleProjectStatusRefresh();
    } else if (event.event.case === "gitSync") {
      const { project, operation, running } = event.event.value;
      if (!project || !operation) return;
      setGitSync((m) => {
        const prev = m[project] ?? "";
        const next = running ? operation : "";
        if (prev === next) return m;
        return { ...m, [project]: next };
      });
    } else if (event.event.case === "gitChanged") {
      const { project } = event.event.value;
      if (!project) return;
      fetchGitStatus(project);
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
