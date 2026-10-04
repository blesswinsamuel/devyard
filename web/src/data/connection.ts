import { createStore } from "solid-js/store";
import type { WatchResponse } from "~/gen/devyard/v1/control_pb";
import { backoffDelay, DEFAULT_BACKOFF, sleep as defaultSleep, type BackoffOptions } from "~/lib/backoff";
import { errorMessage } from "./errors";

/**
 * Watch lifecycle: one long-lived server stream. The first message of every
 * stream is a snapshot that replaces all client state, so reconnecting is
 * always safe — no replay bookkeeping on the client.
 *
 *   connecting ──first message──▶ live ──error/end/heartbeat timeout──▶ reconnecting
 *        ▲                                                                  │
 *        └──────────────────── jittered backoff (500ms → 8s) ◀──────────────┘
 */
export type ConnectionPhase = "connecting" | "live" | "reconnecting";

export interface ConnectionState {
  phase: ConnectionPhase;
  /** Consecutive failed attempts since the last live stream. */
  attempt: number;
  lastError: string;
  /** Epoch ms of the next retry while reconnecting. */
  retryAt: number;
  /** Whether a stream has ever been live in this page. */
  everConnected: boolean;
}

export interface WatchConnectionOptions {
  open: (signal: AbortSignal) => AsyncIterable<WatchResponse>;
  onMessage: (msg: WatchResponse) => void;
  /** Called when a stream delivers its first message. */
  onLive?: (reconnected: boolean) => void;
  /** Called when a stream fails or ends (before the retry is scheduled). */
  onError?: (err: unknown) => void;
  backoff?: BackoffOptions;
  /** Abort and reconnect when nothing (not even a heartbeat) arrives for this long. */
  heartbeatTimeoutMs?: number;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  now?: () => number;
}

export function createWatchConnection(opts: WatchConnectionOptions) {
  const [state, setState] = createStore<ConnectionState>({
    phase: "connecting",
    attempt: 0,
    lastError: "",
    retryAt: 0,
    everConnected: false,
  });
  const sleep = opts.sleep ?? defaultSleep;
  const now = opts.now ?? Date.now;
  const heartbeatTimeout = opts.heartbeatTimeoutMs ?? 30_000;
  let running = false;
  let disposed = false;
  let stream: AbortController | null = null;
  let wake: AbortController | null = null;

  async function loop() {
    let attempt = 0;
    while (!disposed) {
      const ac = new AbortController();
      stream = ac;
      let timer: ReturnType<typeof setTimeout> | undefined;
      const arm = () => {
        clearTimeout(timer);
        timer = setTimeout(() => ac.abort(new Error("no heartbeat from daemon")), heartbeatTimeout);
      };
      let error = "";
      try {
        arm();
        let first = true;
        for await (const msg of opts.open(ac.signal)) {
          arm();
          if (first) {
            first = false;
            attempt = 0;
            const reconnected = state.everConnected;
            setState({ phase: "live", attempt: 0, lastError: "", retryAt: 0, everConnected: true });
            opts.onLive?.(reconnected);
          }
          opts.onMessage(msg);
        }
        error = "stream closed by daemon";
      } catch (err) {
        opts.onError?.(err);
        error = ac.signal.aborted && ac.signal.reason instanceof Error ? ac.signal.reason.message : errorMessage(err);
      } finally {
        clearTimeout(timer);
      }
      if (disposed) break;
      attempt++;
      const delay = backoffDelay(attempt, opts.backoff ?? DEFAULT_BACKOFF);
      setState({ phase: "reconnecting", attempt, lastError: error, retryAt: now() + delay });
      wake = new AbortController();
      await sleep(delay, wake.signal);
      wake = null;
    }
  }

  return {
    state,
    start() {
      if (running) return;
      running = true;
      void loop();
    },
    /** Skip the remaining backoff delay. */
    retryNow() {
      wake?.abort();
    },
    dispose() {
      disposed = true;
      stream?.abort();
      wake?.abort();
    },
  };
}

export type WatchConnection = ReturnType<typeof createWatchConnection>;
