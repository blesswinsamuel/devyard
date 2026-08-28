import { createSignal } from "solid-js";

export type WSStatus = "connecting" | "open" | "closed";

const [wsStatus, setWsStatus] = createSignal<WSStatus>("connecting");
const [reconnectAttempt, setReconnectAttempt] = createSignal(0);
export { wsStatus, reconnectAttempt };

const MIN_BACKOFF = 500;
const MAX_BACKOFF = 8000;
const CONNECT_WATCHDOG = 5000;

let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let watchdogTimer: ReturnType<typeof setTimeout> | null = null;
let attempt = 0;

interface TerminalSub {
  project?: string;
  cols?: number;
  rows?: number;
  onOutput: (output: string) => void;
  onExit: () => void;
}
const terminalHandlers = new Map<string, TerminalSub>();

interface TerminalRequest {
  type: string;
  id?: string;
  project?: string;
  data?: string;
  cols?: number;
  rows?: number;
}

interface TerminalResponse {
  type: string;
  id?: string;
  output?: string;
  error?: string;
}

let pending: TerminalRequest[] = [];

function backoffMs(): number {
  const base = Math.min(MAX_BACKOFF, MIN_BACKOFF * 2 ** attempt);
  return base / 2 + Math.random() * (base / 2);
}

function scheduleReconnect() {
  if (reconnectTimer != null) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectWS();
  }, backoffMs());
}

function clearWatchdog() {
  if (watchdogTimer != null) {
    clearTimeout(watchdogTimer);
    watchdogTimer = null;
  }
}

export function connectWS() {
  if (ws) {
    const state = ws.readyState;
    if (state !== WebSocket.CLOSING) return;
    if (reconnectTimer == null) scheduleReconnect();
    return;
  }

  setWsStatus("connecting");
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const socket = new WebSocket(`${proto}//${location.host}/ws`);
  ws = socket;

  clearWatchdog();
  watchdogTimer = setTimeout(() => {
    if (ws === socket && socket.readyState === WebSocket.CONNECTING) {
      socket.close();
    }
  }, CONNECT_WATCHDOG);

  socket.onopen = () => {
    if (ws !== socket) return;
    clearWatchdog();
    attempt = 0;
    setReconnectAttempt(0);
    setWsStatus("open");

    const queued = pending;
    pending = [];
    for (const req of queued) {
      socket.send(JSON.stringify(req));
    }

    for (const [id, entry] of terminalHandlers.entries()) {
      if (entry.project && entry.cols && entry.rows) {
        socket.send(
          JSON.stringify({
            type: "spawn_terminal",
            id,
            project: entry.project,
            cols: entry.cols,
            rows: entry.rows,
          })
        );
      }
    }
  };

  socket.onclose = () => {
    if (ws !== socket) return;
    ws = null;
    clearWatchdog();
    setWsStatus("closed");
    attempt++;
    setReconnectAttempt(attempt);
    scheduleReconnect();
  };

  socket.onerror = () => {
    if (ws === socket) setWsStatus("closed");
  };

  socket.onmessage = (e) => {
    let resp: TerminalResponse;
    try {
      resp = JSON.parse(e.data) as TerminalResponse;
    } catch {
      return;
    }
    if (resp.type === "terminal_output" && resp.id) {
      terminalHandlers.get(resp.id)?.onOutput(resp.output ?? "");
    } else if (resp.type === "terminal_exit" && resp.id) {
      terminalHandlers.get(resp.id)?.onExit();
    }
  };
}

function sendWS(req: TerminalRequest) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(req));
    return;
  }
  pending.push(req);
  connectWS();
}

export function subscribeTerminal(
  id: string,
  onOutput: (output: string) => void,
  onExit: () => void
) {
  const existing = terminalHandlers.get(id);
  terminalHandlers.set(id, { ...existing, onOutput, onExit });
  return () => {
    terminalHandlers.delete(id);
  };
}

export function spawnTerminal(id: string, project: string, cols: number, rows: number) {
  const existing = terminalHandlers.get(id);
  if (existing) {
    existing.project = project;
    existing.cols = cols;
    existing.rows = rows;
  }
  sendWS({ type: "spawn_terminal", id, project, cols, rows });
}

export function sendTerminalInput(id: string, data: string) {
  sendWS({ type: "terminal_input", id, data });
}

export function resizeTerminal(id: string, cols: number, rows: number) {
  const existing = terminalHandlers.get(id);
  if (existing) {
    existing.cols = cols;
    existing.rows = rows;
  }
  sendWS({ type: "terminal_resize", id, cols, rows });
}

export function closeTerminal(id: string) {
  terminalHandlers.delete(id);
  sendWS({ type: "close_terminal", id });
}
