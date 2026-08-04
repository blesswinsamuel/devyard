import { createSignal } from "solid-js";
import type { WSRequest, WSResponse } from "./types";

export type WSStatus = "connecting" | "open" | "closed";

const [wsStatus, setWsStatus] = createSignal<WSStatus>("connecting");
export { wsStatus };

let ws: WebSocket | null = null;
const handlers = new Map<string, (resp: WSResponse) => void>();
const openHandlers = new Set<() => void>();
const logHandlers = new Map<
  string,
  {
    project: string;
    service: string;
    onLine: (line: string) => void;
    onRotate?: () => void;
  }
>();
/** Outbound messages buffered while the socket is connecting or reconnecting. */
let pending: WSRequest[] = [];
/** True after the first successful open; used to distinguish reconnects. */
let hasOpened = false;

function logKey(project: string, service: string) {
  return `${project}\0${service}`;
}

export function connectWS() {
  if (ws && ws.readyState <= WebSocket.OPEN) return;

  setWsStatus("connecting");
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  ws = new WebSocket(`${proto}//${location.host}/ws`);

  ws.onopen = () => {
    setWsStatus("open");
    const queued = pending;
    pending = [];
    for (const req of queued) {
      ws!.send(JSON.stringify(req));
    }
    // Server-side subscriptions die with the old socket; re-issue them.
    for (const entry of logHandlers.values()) {
      ws!.send(
        JSON.stringify({
          type: "subscribe_logs",
          project: entry.project,
          service: entry.service,
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
  ws.onclose = () => {
    ws = null;
    setWsStatus("closed");
    setTimeout(() => connectWS(), 2000);
  };
  ws.onerror = () => {
    // onclose follows; avoid a duplicate reconnect timer.
    setWsStatus("closed");
  };

  ws.onmessage = (e) => {
    const resp: WSResponse = JSON.parse(e.data);
    if (resp.type === "log_line") {
      const key = logKey(resp.project!, resp.service!);
      logHandlers.get(key)?.onLine(resp.line!);
    } else if (resp.type === "log_rotated") {
      // A new run started; the previous run's lines are over. Let the
      // subscriber reset its view so only the fresh run is shown.
      const key = logKey(resp.project!, resp.service!);
      logHandlers.get(key)?.onRotate?.();
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
  pending.push(req);
  connectWS();
}

export function onWS(type: string, handler: (resp: WSResponse) => void) {
  handlers.set(type, handler);
  return () => handlers.delete(type);
}

/** Fires on every successful WS open (including reconnects). */
export function onWSOpen(handler: () => void) {
  openHandlers.add(handler);
  return () => openHandlers.delete(handler);
}

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void,
  onRotate?: () => void
) {
  const key = logKey(project, service);
  logHandlers.set(key, { project, service, onLine, onRotate });
  sendWS({ type: "subscribe_logs", project, service });
  return () => {
    const current = logHandlers.get(key);
    // Only tear down if we still own the slot (a newer subscribe may have replaced us).
    if (current?.onLine === onLine) {
      logHandlers.delete(key);
      sendWS({ type: "unsubscribe_logs", project, service });
    }
  };
}
