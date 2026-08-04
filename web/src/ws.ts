import { createSignal } from "solid-js";
import type { WSRequest, WSResponse } from "./types";

export type WSStatus = "connecting" | "open" | "closed";

const [wsStatus, setWsStatus] = createSignal<WSStatus>("connecting");
export { wsStatus };

let ws: WebSocket | null = null;
const handlers = new Map<string, (resp: WSResponse) => void>();
const logHandlers = new Map<string, (line: string) => void>();
/** Outbound messages buffered while the socket is connecting or reconnecting. */
let pending: WSRequest[] = [];

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
      const key = `${resp.project}/${resp.service}`;
      logHandlers.get(key)?.(resp.line!);
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

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void
) {
  const key = `${project}/${service}`;
  logHandlers.set(key, onLine);
  sendWS({ type: "subscribe_logs", project, service });
  return () => {
    logHandlers.delete(key);
    sendWS({ type: "unsubscribe_logs", project, service });
  };
}
