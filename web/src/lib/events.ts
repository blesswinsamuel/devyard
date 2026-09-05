import { createSignal } from "solid-js";
import { rpcClient } from "~/lib/rpc";
import type { Event } from "~/gen/localcompose/v1/control_pb";

export type EventStreamStatus = "connecting" | "open" | "closed";

const [eventStatus, setEventStatus] = createSignal<EventStreamStatus>("connecting");
const [reconnectAttempt, setReconnectAttempt] = createSignal(0);

export { eventStatus, reconnectAttempt };

type EventHandler = (event: Event) => void;
const eventHandlers = new Set<EventHandler>();

export function onDaemonEvent(handler: EventHandler): () => void {
  eventHandlers.add(handler);
  return () => eventHandlers.delete(handler);
}

type OpenHandler = () => void;
const openHandlers = new Set<OpenHandler>();

export function onConnectionOpen(handler: OpenHandler): () => void {
  openHandlers.add(handler);
  return () => openHandlers.delete(handler);
}

let activeController: AbortController | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let attempt = 0;
let started = false;

const MIN_BACKOFF = 500;
const MAX_BACKOFF = 8000;

function backoffMs(): number {
  const base = Math.min(MAX_BACKOFF, MIN_BACKOFF * 2 ** attempt);
  return base / 2 + Math.random() * (base / 2);
}

async function subscribeLoop() {
  if (!started) return;

  if (activeController) {
    activeController.abort();
  }
  activeController = new AbortController();
  const signal = activeController.signal;

  setEventStatus("connecting");

  try {
    const stream = rpcClient.subscribeEvents({}, { signal });
    attempt = 0;
    setReconnectAttempt(0);

    let isFirst = true;
    for await (const event of stream) {
      if (signal.aborted) break;
      if (isFirst) {
        isFirst = false;
        setEventStatus("open");
        for (const handler of openHandlers) {
          try {
            handler();
          } catch (e) {
            console.error("Connection open handler error:", e);
          }
        }
      }
      for (const handler of eventHandlers) {
        try {
          handler(event);
        } catch (e) {
          console.error("Event handler error:", e);
        }
      }
    }
  } catch (err: unknown) {
    if (signal.aborted) return;
    // stream ended or failed
  } finally {
    if (!signal.aborted && started) {
      setEventStatus("closed");
      attempt++;
      setReconnectAttempt(attempt);
      scheduleReconnect();
    }
  }
}

function scheduleReconnect() {
  if (reconnectTimer != null) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    subscribeLoop();
  }, backoffMs());
}

export function startEvents() {
  if (started) return;
  started = true;
  subscribeLoop();
}

export function stopEvents() {
  started = false;
  if (reconnectTimer != null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (activeController) {
    activeController.abort();
    activeController = null;
  }
  setEventStatus("closed");
}
