import { createRoot, createMemo } from "solid-js";
import { createStore, produce, reconcile, unwrap } from "solid-js/store";
import type {
  DaemonInfo,
  GitStatus,
  Project,
  Service,
  Snapshot,
  Task,
  WatchResponse,
} from "~/gen/devyard/v1/control_pb";

// -----------------------------------------------------------------------------
// Plain entity shapes. Protobuf messages are class instances with bigint
// fields; the store holds plain objects (so reconcile can diff them) with
// millisecond numbers.
// -----------------------------------------------------------------------------

export type ProjectStatus = "stopped" | "starting" | "running" | "degraded" | "stopping" | "error";
export type ProjectDesired = "running" | "stopped" | "partial";
export type ServiceStatus =
  | "stopped"
  | "waiting"
  | "building"
  | "starting"
  | "running"
  | "stopping"
  | "backoff"
  | "exited"
  | "failed";
export type HealthStatus = "" | "starting" | "healthy" | "unhealthy";
export type TaskStatus = "idle" | "waiting" | "running" | "stopping" | "exited" | "failed";

export interface DaemonEntity {
  pid: number;
  startedAt: number;
  version: string;
  goVersion: string;
  goroutines: number;
  memoryRss: number;
  memoryHeap: number;
  webAddr: string;
  proxyAddr: string;
  proxyTlsAddr: string;
  domainSuffix: string;
  draining: boolean;
  /** What the daemon could not apply of the global config; empty when applied. */
  configError: string;
}

export interface ProjectEntity {
  id: string;
  configPath: string;
  /** False for a project without a devyard.yml (git view and terminals only). */
  hasConfig: boolean;
  /** Index in the global config's project list; the sidebar order. */
  position: number;
  /** Project env files that exist, in load order. */
  envFiles: string[];
  status: ProjectStatus;
  desired: ProjectDesired;
  error: string;
  servicesTotal: number;
  servicesRunning: number;
  /** The service served at the project's own hostname. */
  primary: string;
  links: { name: string; url: string }[];
  updatedAt: number;
}

export interface ReadyEntity {
  kind: "http" | "tcp" | "exec" | string;
  /** The URL, address or command the probe checks. */
  target: string;
  intervalMs: number;
  timeoutMs: number;
  retries: number;
  startPeriodMs: number;
}

export interface ServiceSpecEntity {
  command: string;
  dir: string;
  restart: string;
  /** Services this one waits for (until they are ready). */
  dependsOn: string[];
  ready: ReadyEntity | null;
  ports: { name: string; port: number; auto: boolean }[];
  tty: boolean;
  envKeys: string[];
  buildCommand: string;
  autostart: boolean;
  stopSignal: string;
}

export interface ServiceEntity {
  project: string;
  name: string;
  status: ServiceStatus;
  health: HealthStatus;
  healthDetail: string;
  pid: number;
  exitCode: number;
  restarts: number;
  run: number;
  startedAt: number;
  finishedAt: number;
  message: string;
  urls: string[];
  spec: ServiceSpecEntity;
  nextRestartAt: number;
  /** Position in the project's dependency order. */
  order: number;
}

export interface TaskSpecEntity {
  command: string;
  dir: string;
  tty: boolean;
  dependsOn: string[];
  envKeys: string[];
}

export interface TaskEntity {
  project: string;
  name: string;
  status: TaskStatus;
  pid: number;
  exitCode: number;
  run: number;
  startedAt: number;
  finishedAt: number;
  message: string;
  args: string[];
  spec: TaskSpecEntity;
}

export interface GitEntity {
  project: string;
  isRepo: boolean;
  branch: string;
  upstream: string;
  ahead: number;
  behind: number;
  staged: number;
  dirty: number;
  untracked: number;
  conflicts: number;
  isClean: boolean;
  headHash: string;
  syncOperation: "" | "pull" | "fetch" | "push" | string;
  changeSeq: number;
}

export interface EntityState {
  revision: number;
  /** True once the first snapshot has been applied. */
  loaded: boolean;
  daemon: DaemonEntity | null;
  projects: Record<string, ProjectEntity>;
  services: Record<string, ServiceEntity>;
  tasks: Record<string, TaskEntity>;
  git: Record<string, GitEntity>;
}

/** Key for services and tasks: "<project>/<name>". */
export const entityKey = (project: string, name: string) => `${project}/${name}`;

const num = (v: bigint | number | undefined) => (v === undefined ? 0 : Number(v));

export function toDaemon(d: DaemonInfo): DaemonEntity {
  return {
    pid: d.pid,
    startedAt: num(d.startedAtUnixMs),
    version: d.version,
    goVersion: d.goVersion,
    goroutines: d.goroutines,
    memoryRss: num(d.memoryRss),
    memoryHeap: num(d.memoryHeap),
    webAddr: d.webAddr,
    proxyAddr: d.proxyAddr,
    proxyTlsAddr: d.proxyTlsAddr,
    domainSuffix: d.domainSuffix,
    draining: d.draining,
    configError: d.configError,
  };
}

export function toProject(p: Project): ProjectEntity {
  return {
    id: p.id,
    configPath: p.configPath,
    hasConfig: p.hasConfig,
    position: p.position,
    envFiles: [...p.envFiles],
    status: (p.status || "stopped") as ProjectStatus,
    desired: (p.desired || "stopped") as ProjectDesired,
    error: p.error,
    servicesTotal: p.servicesTotal,
    servicesRunning: p.servicesRunning,
    primary: p.primary,
    links: p.links.map((l) => ({ name: l.name, url: l.url })),
    updatedAt: num(p.updatedAtUnixMs),
  };
}

export function toService(s: Service): ServiceEntity {
  const spec = s.spec;
  const ready = spec?.ready;
  return {
    project: s.project,
    name: s.name,
    status: (s.status || "stopped") as ServiceStatus,
    health: s.health as HealthStatus,
    healthDetail: s.healthDetail,
    pid: s.pid,
    exitCode: s.exitCode,
    restarts: s.restarts,
    run: num(s.run),
    startedAt: num(s.startedAtUnixMs),
    finishedAt: num(s.finishedAtUnixMs),
    message: s.message,
    urls: [...s.urls],
    nextRestartAt: num(s.nextRestartAtUnixMs),
    order: s.order,
    spec: {
      command: spec?.command ?? "",
      dir: spec?.dir ?? "",
      restart: spec?.restart ?? "",
      dependsOn: [...(spec?.dependsOn ?? [])],
      ready:
        ready && ready.kind
          ? {
              kind: ready.kind,
              target: ready.target,
              intervalMs: num(ready.intervalMs),
              timeoutMs: num(ready.timeoutMs),
              retries: ready.retries,
              startPeriodMs: num(ready.startPeriodMs),
            }
          : null,
      ports: (spec?.ports ?? []).map((p) => ({ name: p.name, port: p.port, auto: p.auto })),
      tty: spec?.tty ?? false,
      envKeys: [...(spec?.envKeys ?? [])],
      buildCommand: spec?.buildCommand ?? "",
      autostart: spec?.autostart ?? true,
      stopSignal: spec?.stopSignal ?? "",
    },
  };
}

export function toTask(t: Task): TaskEntity {
  const spec = t.spec;
  return {
    project: t.project,
    name: t.name,
    status: (t.status || "idle") as TaskStatus,
    pid: t.pid,
    exitCode: t.exitCode,
    run: num(t.run),
    startedAt: num(t.startedAtUnixMs),
    finishedAt: num(t.finishedAtUnixMs),
    message: t.message,
    args: [...t.args],
    spec: {
      command: spec?.command ?? "",
      dir: spec?.dir ?? "",
      tty: spec?.tty ?? true,
      dependsOn: [...(spec?.dependsOn ?? [])],
      envKeys: [...(spec?.envKeys ?? [])],
    },
  };
}

export function toGit(g: GitStatus): GitEntity {
  return {
    project: g.project,
    isRepo: g.isRepo,
    branch: g.branch,
    upstream: g.upstream,
    ahead: g.ahead,
    behind: g.behind,
    staged: g.staged,
    dirty: g.dirty,
    untracked: g.untracked,
    conflicts: g.conflicts,
    isClean: g.isClean,
    headHash: g.headHash,
    syncOperation: g.syncOperation,
    changeSeq: num(g.changeSeq),
  };
}

function emptyState(): EntityState {
  return { revision: 0, loaded: false, daemon: null, projects: {}, services: {}, tasks: {}, git: {} };
}

function fromSnapshot(snap: Snapshot, revision: number): EntityState {
  const state = emptyState();
  state.revision = revision;
  state.loaded = true;
  state.daemon = snap.daemon ? toDaemon(snap.daemon) : null;
  for (const p of snap.projects) state.projects[p.id] = toProject(p);
  for (const s of snap.services) state.services[entityKey(s.project, s.name)] = toService(s);
  for (const t of snap.tasks) state.tasks[entityKey(t.project, t.name)] = toTask(t);
  for (const g of snap.git) state.git[g.project] = toGit(g);
  return state;
}

// -----------------------------------------------------------------------------
// Store
// -----------------------------------------------------------------------------

/** Emitted for incremental changes only (never for snapshots), so consumers
 * like crash toasts react to live transitions, not to initial state. */
export type EntityEvent =
  | { kind: "service"; prev: ServiceEntity | undefined; next: ServiceEntity }
  | { kind: "task"; prev: TaskEntity | undefined; next: TaskEntity }
  | { kind: "git"; prev: GitEntity | undefined; next: GitEntity }
  | { kind: "snapshot" };

export function createEntityStore() {
  const [state, setState] = createStore<EntityState>(emptyState());
  const listeners = new Set<(e: EntityEvent) => void>();
  const emit = (e: EntityEvent) => listeners.forEach((l) => l(e));
  const copy = <T extends object>(v: T | undefined): T | undefined => (v ? { ...unwrap(v) } : undefined);

  function apply(msg: WatchResponse): void {
    const revision = Number(msg.revision);
    const ev = msg.event;
    if (ev.case === "snapshot") {
      setState(reconcile(fromSnapshot(ev.value, revision)));
      emit({ kind: "snapshot" });
      return;
    }
    if (ev.case !== "change") return;
    // Deltas at or below the applied revision are duplicates of what the
    // snapshot already contains.
    if (revision && revision <= state.revision) return;
    const change = ev.value.change;
    switch (change.case) {
      case "project": {
        const p = toProject(change.value);
        setState("projects", p.id, reconcile(p));
        break;
      }
      case "service": {
        const s = toService(change.value);
        const key = entityKey(s.project, s.name);
        const prev = copy(state.services[key]);
        setState("services", key, reconcile(s));
        emit({ kind: "service", prev, next: s });
        break;
      }
      case "task": {
        const t = toTask(change.value);
        const key = entityKey(t.project, t.name);
        const prev = copy(state.tasks[key]);
        setState("tasks", key, reconcile(t));
        emit({ kind: "task", prev, next: t });
        break;
      }
      case "git": {
        const g = toGit(change.value);
        const prev = copy(state.git[g.project]);
        setState("git", g.project, reconcile(g));
        emit({ kind: "git", prev, next: g });
        break;
      }
      case "daemon":
        setState("daemon", reconcile(toDaemon(change.value)));
        break;
      case "removed": {
        const { kind, project, name } = change.value;
        setState(
          produce((s) => {
            if (kind === "project") {
              // A removed project takes its children with it.
              delete s.projects[project];
              delete s.git[project];
              for (const k of Object.keys(s.services)) if (s.services[k]!.project === project) delete s.services[k];
              for (const k of Object.keys(s.tasks)) if (s.tasks[k]!.project === project) delete s.tasks[k];
            } else if (kind === "service") delete s.services[entityKey(project, name)];
            else if (kind === "task") delete s.tasks[entityKey(project, name)];
            else if (kind === "git") delete s.git[project];
          }),
        );
        break;
      }
      default:
        return;
    }
    if (revision) setState("revision", revision);
  }

  function subscribe(listener: (e: EntityEvent) => void): () => void {
    listeners.add(listener);
    return () => listeners.delete(listener);
  }

  return { state, apply, subscribe };
}

export type EntityStore = ReturnType<typeof createEntityStore>;

// -----------------------------------------------------------------------------
// App singleton + selectors
// -----------------------------------------------------------------------------

export const entities = createEntityStore();
const state = entities.state;

const byName = <T extends { name: string }>(a: T, b: T) => a.name.localeCompare(b.name);

/** Projects in list order: the global config's order, then by id. */
export const sortProjects = (projects: ProjectEntity[]): ProjectEntity[] =>
  [...projects].sort((a, b) => a.position - b.position || a.id.localeCompare(b.id));

const selectors = createRoot(() => {
  const projectList = createMemo(() => sortProjects(Object.values(state.projects)));
  const servicesByProject = createMemo(() => {
    const map: Record<string, ServiceEntity[]> = {};
    for (const s of Object.values(state.services)) (map[s.project] ??= []).push(s);
    // Config (dependency) order, then name.
    for (const list of Object.values(map)) list.sort((a, b) => a.order - b.order || byName(a, b));
    return map;
  });
  const tasksByProject = createMemo(() => {
    const map: Record<string, TaskEntity[]> = {};
    for (const t of Object.values(state.tasks)) (map[t.project] ??= []).push(t);
    for (const list of Object.values(map)) list.sort(byName);
    return map;
  });
  return { projectList, servicesByProject, tasksByProject };
});

const EMPTY: never[] = [];

export const projectList = () => selectors.projectList();
export const servicesOf = (project: string): ServiceEntity[] => selectors.servicesByProject()[project] ?? EMPTY;
export const tasksOf = (project: string): TaskEntity[] => selectors.tasksByProject()[project] ?? EMPTY;
export const getProject = (project: string): ProjectEntity | undefined => state.projects[project];
export const getService = (project: string, name: string): ServiceEntity | undefined =>
  state.services[entityKey(project, name)];
export const getTask = (project: string, name: string): TaskEntity | undefined =>
  state.tasks[entityKey(project, name)];
export const getGit = (project: string): GitEntity | undefined => state.git[project];
