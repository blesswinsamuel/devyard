export interface ProjectInfo {
  name: string;
  status: string;
  config_path: string;
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

export interface WSRequest {
  type: string;
  project?: string;
  service?: string;
  config_path?: string;
}

export interface WSResponse {
  type: string;
  project?: string;
  service?: string;
  data?: ProjectInfo[] | ServiceState[];
  line?: string;
  ok?: boolean;
  error?: string;
}
