export interface ProjectInfo {
  name: string;
  status: string;
  config_path: string;
  running_services?: number;
  total_services?: number;
}

export interface ServiceState {
  name: string;
  status: string;
  pid: number;
  exit_code: number;
  restarts: number;
  started_at?: string;
  finished_at?: string;
  has_health: boolean;
  health: string;
}

export interface ActionInfo {
  name: string;
  command: string;
  working_dir?: string;
  depends_on?: string[];
}

export interface ActionState {
  name: string;
  command: string;
  status: string;
  pid: number;
  exit_code: number;
  started_at?: string;
  finished_at?: string;
}

export interface WSRequest {
  type: string;
  project?: string;
  service?: string;
  action?: string;
  args?: string[];
  config_path?: string;
  signal?: string;
  env_file?: string;
  id?: string;
  data?: string;
  cols?: number;
  rows?: number;
}

export interface WSResponse {
  type: string;
  project?: string;
  service?: string;
  action?: string;
  data?: ProjectInfo[] | ServiceState[] | ActionInfo[];
  line?: string;
  ok?: boolean;
  error?: string;
  exit_code?: number;
  id?: string;
  output?: string;
}

export type ViewMode = "logs" | "shell" | "git" | "agents";

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
