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
  LogChunk,
  Event,
} from "~/gen/localcompose/v1/control_pb";

export type ViewMode = "logs" | "shell" | "git";

export interface TerminalPaneNode {
  type: "terminal";
  id: string;
}

export interface SplitPaneNode {
  type: "split";
  id: string;
  direction: "horizontal" | "vertical";
  children: PaneNode[];
  sizes?: number[];
}

export type PaneNode = TerminalPaneNode | SplitPaneNode;

export interface ShellTab {
  id: string;
  title: string;
  rootPane: PaneNode;
}
