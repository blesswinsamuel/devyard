import { createSignal } from "solid-js";
import type { WSRequest, WSResponse } from "./types";

export type WSStatus = "connecting" | "open" | "closed";

const [wsStatus, setWsStatus] = createSignal<WSStatus>("connecting");
export { wsStatus };

let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
const handlers = new Map<string, (resp: WSResponse) => void>();
const openHandlers = new Set<() => void>();
const logHandlers = new Map<
  string,
  {
    project: string;
    service: string;
    prev: boolean;
    onLine: (line: string) => void;
    onRotate?: () => void;
  }
>();
const actionLogHandlers = new Map<
  string,
  {
    project: string;
    action: string;
    prev: boolean;
    onLine: (line: string) => void;
    onRotate?: () => void;
  }
>();
/** Outbound messages buffered while the socket is connecting or reconnecting. */
let pending: WSRequest[] = [];
/** True after the first successful open; used to distinguish reconnects. */
let hasOpened = false;

function logKey(project: string, serviceOrAction: string, prev = false) {
  return `${project}\0${serviceOrAction}\0${prev}`;
}

function enqueuePending(req: WSRequest) {
  // Coalesce refresh intents so a long outage doesn't burst hundreds of polls.
  if (req.type === "list_projects") {
    pending = pending.filter((p) => p.type !== "list_projects");
  } else if (req.type === "list_services" && req.project) {
    pending = pending.filter(
      (p) => !(p.type === "list_services" && p.project === req.project)
    );
  }
  pending.push(req);
}

function scheduleReconnect() {
  if (reconnectTimer != null) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectWS();
  }, 2000);
}

export function connectWS() {
  if (ws) {
    const state = ws.readyState;
    if (state === WebSocket.CONNECTING || state === WebSocket.OPEN) return;
    // CLOSING: wait for onclose to clear and schedule reconnect.
    if (state === WebSocket.CLOSING) return;
  }

  setWsStatus("connecting");
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const socket = new WebSocket(`${proto}//${location.host}/ws`);
  ws = socket;

  socket.onopen = () => {
    if (ws !== socket) return;
    setWsStatus("open");
    const queued = pending;
    pending = [];
    for (const req of queued) {
      socket.send(JSON.stringify(req));
    }
    // Server-side subscriptions die with the old socket; clear local views
    // then re-issue so history isn't appended twice.
    for (const entry of logHandlers.values()) {
      entry.onRotate?.();
      socket.send(
        JSON.stringify({
          type: "subscribe_logs",
          project: entry.project,
          service: entry.service,
          prev: entry.prev,
        })
      );
    }
    for (const entry of actionLogHandlers.values()) {
      entry.onRotate?.();
      socket.send(
        JSON.stringify({
          type: "subscribe_action_logs",
          project: entry.project,
          action: entry.action,
          prev: entry.prev,
        })
      );
    }
    // Reconnect only — first open is covered by the caller's initial fetch
    // (already flushed from `pending` above).
    if (hasOpened) {
      for (const h of openHandlers) {
        h();
      }
    }
    hasOpened = true;
  };
  socket.onclose = () => {
    if (ws !== socket) return;
    ws = null;
    setWsStatus("closed");
    scheduleReconnect();
  };
  socket.onerror = () => {
    // onclose follows; avoid a duplicate reconnect timer.
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
        const h = actionLogHandlers.get(logKey(resp.project!, resp.action, !!resp.prev));
        if (h) h.onLine(resp.line!);
      } else {
        const h = logHandlers.get(logKey(resp.project!, resp.service!, !!resp.prev));
        if (h) h.onLine(resp.line!);
      }
    } else if (resp.type === "log_rotated") {
      // A new run started; the previous run's lines are over. Let the
      // subscriber reset its view so only the fresh run is shown.
      if (resp.action) {
        actionLogHandlers.get(logKey(resp.project!, resp.action, !!resp.prev))?.onRotate?.();
      } else {
        logHandlers.get(logKey(resp.project!, resp.service!, !!resp.prev))?.onRotate?.();
      }
    } else if (resp.type === "terminal_output") {
      terminalHandlers.get(resp.id!)?.onOutput(resp.output!);
    } else if (resp.type === "terminal_exit") {
      terminalHandlers.get(resp.id!)?.onExit();
    } else {
      handlers.get(resp.type)?.(resp);
    }
  };
}

export function sendWS(req: WSRequest) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(req));
    return;
  }
  enqueuePending(req);
  connectWS();
}

export function onWS(type: string, handler: (resp: WSResponse) => void) {
  handlers.set(type, handler);
  return () => handlers.delete(type);
}

/** Fires on reconnect opens (not the first successful open). */
export function onWSOpen(handler: () => void) {
  openHandlers.add(handler);
  return () => openHandlers.delete(handler);
}

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
) {
  const key = logKey(project, service, prev);
  logHandlers.set(key, { project, service, prev, onLine, onRotate });
  sendWS({ type: "subscribe_logs", project, service, prev });
  return () => {
    const current = logHandlers.get(key);
    // Only tear down if we still own the slot (a newer subscribe may have replaced us).
    if (current?.onLine === onLine) {
      logHandlers.delete(key);
      sendWS({ type: "unsubscribe_logs", project, service, prev });
    }
  };
}

export function subscribeActionLogs(
  project: string,
  action: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false
) {
  const key = logKey(project, action, prev);
  actionLogHandlers.set(key, { project, action, prev, onLine, onRotate });
  sendWS({ type: "subscribe_action_logs", project, action, prev });
  return () => {
    const current = actionLogHandlers.get(key);
    // Only tear down if we still own the slot (a newer subscribe may have replaced us).
    if (current?.onLine === onLine) {
      actionLogHandlers.delete(key);
      sendWS({ type: "unsubscribe_action_logs", project, action, prev });
    }
  };
}

const terminalHandlers = new Map<
  string,
  { onOutput: (output: string) => void; onExit: () => void }
>();

export function subscribeTerminal(
  id: string,
  onOutput: (output: string) => void,
  onExit: () => void
) {
  terminalHandlers.set(id, { onOutput, onExit });
  return () => {
    terminalHandlers.delete(id);
  };
}

export function spawnTerminal(
  id: string,
  project: string,
  cols: number,
  rows: number
) {
  sendWS({ type: "spawn_terminal", id, project, cols, rows });
}

export function sendTerminalInput(id: string, data: string) {
  sendWS({ type: "terminal_input", id, data });
}

export function resizeTerminal(id: string, cols: number, rows: number) {
  sendWS({ type: "terminal_resize", id, cols, rows });
}

export function closeTerminal(id: string) {
  sendWS({ type: "close_terminal", id });
}

