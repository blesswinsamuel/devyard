export type {
  DaemonInfo,
  ServiceState,
  TaskState,
  ProjectInfo,
  ServiceStat,
  PortBinding,
  GitRef,
  GitBranch,
  GitTag,
  GitStash,
  GitCommit,
  GitFileChange,
  GitDiffResult,
  GitStatus,
  LogChunk,
  Event,
} from "~/gen/devyard/v1/control_pb";

// --- unified workspace model -------------------------------------------------
//
// One global pane tree. Every leaf pane owns a tab strip; tabs are typed
// documents (overview, log, git, terminal) that can live in any pane. Splits
// subdivide the area; a tab can be dragged between panes.

export interface OverviewTab {
  kind: "overview";
  /** Stable id `ov:project` — dedupes across panes. */
  id: string;
  project: string;
}

export interface ServiceLogTab {
  kind: "log-service";
  /** Stable id `s:project/service` — dedupes across panes. */
  id: string;
  project: string;
  service: string;
  /** Show the previous run's logs instead of the live stream. */
  previous?: boolean;
}

export interface TaskLogTab {
  kind: "log-task";
  /** Stable id `t:project/task` — dedupes across panes. */
  id: string;
  project: string;
  task: string;
  previous?: boolean;
}

export interface GitTab {
  kind: "git";
  /** Stable id `git:project` — one git tab per project. */
  id: string;
  project: string;
}

export interface TerminalTab {
  kind: "terminal";
  /** Unique id `tab-N`; also names the pty session (`term-N`). */
  id: string;
  project: string;
  termId: string;
}

export type WorkspaceTab = OverviewTab | ServiceLogTab | TaskLogTab | GitTab | TerminalTab;

export interface PaneNode {
  type: "pane";
  id: string;
  tabs: WorkspaceTab[];
  activeTabId: string | null;
}

export interface SplitNode {
  type: "split";
  id: string;
  direction: "horizontal" | "vertical";
  children: WorkspaceNode[];
  sizes?: number[];
}

export type WorkspaceNode = PaneNode | SplitNode;
