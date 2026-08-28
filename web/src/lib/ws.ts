import { createSignal } from "solid-js";
import type { WSRequest, WSResponse } from "~/lib/types";

export type WSStatus = "connecting" | "open" | "closed";

const [wsStatus, setWsStatus] = createSignal<WSStatus>("connecting");
const [reconnectAttempt, setReconnectAttempt] = createSignal(0);
export { wsStatus, reconnectAttempt };

const MIN_BACKOFF = 500;
const MAX_BACKOFF = 8000;
/** Abort a half-open CONNECTING attempt (laptop sleep, dead NAT) after this long. */
const CONNECT_WATCHDOG = 5000;

let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let watchdogTimer: ReturnType<typeof setTimeout> | null = null;
let attempt = 0;
let hasOpened = false;

type ResponseHandler = (resp: WSResponse) => void;
const handlers = new Map<string, Set<ResponseHandler>>();
const openHandlers = new Set<() => void>();

interface LogSub {
  project: string;
  service?: string;
  action?: string;
  prev: boolean;
  onLine: (line: string) => void;
  onRotate?: () => void;
}
const logHandlers = new Map<string, LogSub>();

interface TerminalSub {
  project?: string;
  cols?: number;
  rows?: number;
  onOutput: (output: string) => void;
  onExit: () => void;
}
const terminalHandlers = new Map<string, TerminalSub>();

/**
 * Outbound messages buffered while the socket is down. Every list/refresh
 * intent is coalesced by (type, project) so an outage doesn't queue hundreds
 * of polls. Log subscribes are NOT queued — they are re-issued exactly once
 * by the resubscribe loop on open, so history can never be replayed twice.
 */
let pending: WSRequest[] = [];
const COALESCED_TYPES = new Set([
  "list_projects",
  "list_services",
  "list_actions",
  "list_action_states",
  "list_ports",
  "daemon_status",
]);

function enqueuePending(req: WSRequest) {
  if (COALESCED_TYPES.has(req.type)) {
    pending = pending.filter(
      (p) => !(p.type === req.type && p.project === req.project)
    );
  }
  pending.push(req);
}

function backoffMs(): number {
  // Exponential with jitter, capped.
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
    // CLOSING: wait for onclose to clear and schedule reconnect.
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

    // Server-side subscriptions died with the old socket. Clear local views,
    // then re-issue each subscription once (pending never holds them).
    for (const entry of logHandlers.values()) {
      entry.onRotate?.();
      socket.send(
        JSON.stringify({
          type: entry.action ? "subscribe_action_logs" : "subscribe_logs",
          project: entry.project,
          service: entry.service,
          action: entry.action,
          prev: entry.prev,
        })
      );
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
    // Reconnect only — first open is covered by the initial fetch already
    // flushed from `pending` above.
    if (hasOpened) {
      for (const h of openHandlers) h();
    }
    hasOpened = true;
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
    // onclose follows; avoid a duplicate status flip.
    if (ws === socket) setWsStatus("closed");
  };

  socket.onmessage = (e) => {
    let resp: WSResponse;
    try {
      resp = JSON.parse(e.data) as WSResponse;
    } catch {
      return;
    }
    if (resp.type === "log_line") {
      if (resp.action) {
        logHandlers.get(logKey(resp.project!, undefined, resp.action, !!resp.prev))?.onLine(resp.line!);
      } else {
        logHandlers.get(logKey(resp.project!, resp.service!, undefined, !!resp.prev))?.onLine(resp.line!);
      }
    } else if (resp.type === "log_rotated") {
      if (resp.action) {
        logHandlers.get(logKey(resp.project!, undefined, resp.action, false))?.onRotate?.();
      } else {
        logHandlers.get(logKey(resp.project!, resp.service!, undefined, false))?.onRotate?.();
      }
    } else if (resp.type === "terminal_output") {
      terminalHandlers.get(resp.id!)?.onOutput(resp.output!);
    } else if (resp.type === "terminal_exit") {
      terminalHandlers.get(resp.id!)?.onExit();
    } else {
      for (const h of handlers.get(resp.type) ?? []) h(resp);
    }
  };
}

function logKey(project: string, service: string | undefined, action: string | undefined, prev: boolean): string {
  const target = action ?? service ?? "";
  return `${project}\0${target}\0${prev}`;
}

export function sendWS(req: WSRequest) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(req));
    return;
  }
  enqueuePending(req);
  connectWS();
}

export function onWS(type: string, handler: ResponseHandler) {
  let set = handlers.get(type);
  if (!set) {
    set = new Set();
    handlers.set(type, set);
  }
  set.add(handler);
  return () => set!.delete(handler);
}

/** Fires on reconnect opens (not the first successful open). */
export function onWSOpen(handler: () => void) {
  openHandlers.add(handler);
  return () => openHandlers.delete(handler);
}

function subscribeLog(entry: LogSub): () => void {
  const key = logKey(entry.project, entry.service, entry.action, entry.prev);
  logHandlers.set(key, entry);
  sendWS({
    type: entry.action ? "subscribe_action_logs" : "subscribe_logs",
    project: entry.project,
    service: entry.service,
    action: entry.action,
    prev: entry.prev,
  });
  return () => {
    // Only tear down if we still own the slot (a newer subscribe may have replaced us).
    if (logHandlers.get(key)?.onLine === entry.onLine) {
      logHandlers.delete(key);
      sendWS({
        type: entry.action ? "unsubscribe_action_logs" : "unsubscribe_logs",
        project: entry.project,
        service: entry.service,
        action: entry.action,
        prev: entry.prev,
      });
    }
  };
}

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
) {
  return subscribeLog({ project, service, prev, onLine, onRotate });
}

export function subscribeActionLogs(
  project: string,
  action: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
) {
  return subscribeLog({ project, action, prev, onLine, onRotate });
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
