import { backoffDelay, type BackoffOptions } from "~/lib/backoff";

/**
 * Interactive session client over `/ws/attach` (the browser can't do bidi
 * Connect streams). One WebSocket per session:
 *
 *   client → server  TEXT   {"type":"open","target":{…},"cols","rows"} | {"type":"resize",…}
 *                           | {"type":"eof"} (close the process's stdin) | {"type":"close"} (kill a terminal)
 *   client → server  BINARY raw input bytes
 *   server → client  TEXT   {"type":"ready","session_id","tty","stdin"} | {"type":"exit",…} | {"type":"error","message"}
 *   server → client  BINARY raw output bytes (scrollback replay first on every attach)
 *
 * Unexpected disconnects reattach with backoff (terminals by session_id,
 * tasks/services by name). The server replays scrollback on each attach, so
 * consumers reset their screen on every `ready`.
 */

export type SessionKind = "terminal" | "task" | "service";

export interface SessionTarget {
  kind: SessionKind;
  project: string;
  /** Task/service name; empty for terminals. */
  name?: string;
  /** Existing terminal session to reattach to. */
  sessionId?: string;
}

export type SessionPhase =
  | "connecting"
  | "ready"
  | "reconnecting"
  /** The process exited (exit frame received). */
  | "exited"
  /** The session could not be opened/reattached (e.g. it no longer exists). */
  | "ended"
  /** Detached/terminated by this client. */
  | "closed";

export interface SessionReady {
  sessionId: string;
  /** Raw keystrokes (true) vs line-based stdin (false). */
  tty: boolean;
  /** Whether the process accepts input at all. */
  stdin: boolean;
}

export interface SessionExit {
  exitCode: number;
  message: string;
}

export interface SessionHandlers {
  onOutput?: (data: Uint8Array) => void;
  onReady?: (info: SessionReady) => void;
  onExit?: (info: SessionExit) => void;
  onPhase?: (phase: SessionPhase, detail: string) => void;
}

type WebSocketLike = Pick<WebSocket, "send" | "close" | "readyState" | "binaryType"> & {
  onopen: ((ev: Event) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onclose: ((ev: CloseEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
};

export interface SessionOptions {
  url?: string;
  createSocket?: (url: string) => WebSocketLike;
  /** Reattach after unexpected disconnects (default true). */
  reconnect?: boolean;
  backoff?: BackoffOptions;
}

const OPEN = 1;
const encoder = new TextEncoder();

export function attachUrl(loc: Pick<Location, "protocol" | "host"> = location): string {
  return `${loc.protocol === "https:" ? "wss" : "ws"}://${loc.host}/ws/attach`;
}

export class AttachSession {
  private ws: WebSocketLike | null = null;
  private target: SessionTarget;
  private size: { cols: number; rows: number };
  private phaseValue: SessionPhase = "connecting";
  private readyInfo: SessionReady | null = null;
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  /** Input typed before the session is ready (flushed on ready). */
  private queued: Uint8Array[] = [];
  private done = false;

  constructor(
    target: SessionTarget,
    size: { cols: number; rows: number },
    private handlers: SessionHandlers,
    private opts: SessionOptions = {},
  ) {
    this.target = { ...target };
    this.size = { ...size };
    this.connect();
  }

  get phase(): SessionPhase {
    return this.phaseValue;
  }

  get sessionId(): string | undefined {
    return this.readyInfo?.sessionId ?? this.target.sessionId;
  }

  get tty(): boolean | undefined {
    return this.readyInfo?.tty;
  }

  get stdin(): boolean | undefined {
    return this.readyInfo?.stdin;
  }

  input(data: string | Uint8Array): void {
    const bytes = typeof data === "string" ? encoder.encode(data) : data;
    if (this.phaseValue === "ready" && this.ws?.readyState === OPEN) this.ws.send(bytes);
    else if (!this.done) this.queued.push(bytes);
  }

  resize(cols: number, rows: number): void {
    if (cols <= 0 || rows <= 0) return;
    if (cols === this.size.cols && rows === this.size.rows) return;
    this.size = { cols, rows };
    if (this.phaseValue === "ready") this.sendJson({ type: "resize", cols, rows });
  }

  /** Closes the process's stdin (end of input for line-based sessions). */
  eof(): void {
    if (this.phaseValue === "ready") this.sendJson({ type: "eof" });
  }

  /** Kills a terminal's shell. For task/service sessions this just detaches. */
  terminate(): void {
    if (this.target.kind === "terminal") this.sendJson({ type: "close" });
    this.finish("closed", "");
  }

  /** Closes the connection; the server-side session keeps running. */
  detach(): void {
    this.finish("closed", "");
  }

  private setPhase(phase: SessionPhase, detail = ""): void {
    this.phaseValue = phase;
    this.handlers.onPhase?.(phase, detail);
  }

  private finish(phase: SessionPhase, detail: string): void {
    if (this.done) return;
    this.done = true;
    clearTimeout(this.retryTimer);
    this.queued = [];
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onclose = ws.onmessage = ws.onerror = ws.onopen = null;
      try {
        ws.close();
      } catch {
        // already closed
      }
    }
    this.setPhase(phase, detail);
  }

  private sendJson(msg: unknown): void {
    if (this.ws?.readyState === OPEN) this.ws.send(JSON.stringify(msg));
  }

  private connect(): void {
    const url = this.opts.url ?? attachUrl();
    const ws = this.opts.createSocket ? this.opts.createSocket(url) : (new WebSocket(url) as WebSocketLike);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    let gotReady = false;
    let openedSize = { ...this.size };

    ws.onopen = () => {
      const t = this.target;
      openedSize = { ...this.size };
      this.sendJson({
        type: "open",
        target: { kind: t.kind, project: t.project, name: t.name ?? "", session_id: t.sessionId ?? "" },
        cols: this.size.cols,
        rows: this.size.rows,
      });
    };

    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data !== "string") {
        const data = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : (ev.data as Uint8Array);
        this.handlers.onOutput?.(data);
        return;
      }
      let msg: {
        type?: string;
        session_id?: string;
        tty?: boolean;
        stdin?: boolean;
        exit_code?: number;
        message?: string;
      };
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      switch (msg.type) {
        case "ready": {
          gotReady = true;
          this.attempt = 0;
          // Booleans may be omitted when false.
          this.readyInfo = { sessionId: msg.session_id ?? "", tty: msg.tty === true, stdin: msg.stdin === true };
          // Reattach to the same terminal (not a fresh shell) after drops.
          if (this.target.kind === "terminal" && this.readyInfo.sessionId)
            this.target.sessionId = this.readyInfo.sessionId;
          this.setPhase("ready");
          this.handlers.onReady?.(this.readyInfo);
          // The view may have been resized while the open was in flight.
          if (this.size.cols !== openedSize.cols || this.size.rows !== openedSize.rows)
            this.sendJson({ type: "resize", cols: this.size.cols, rows: this.size.rows });
          const queued = this.queued;
          this.queued = [];
          for (const bytes of queued) ws.send(bytes);
          break;
        }
        case "exit":
          this.handlers.onExit?.({ exitCode: msg.exit_code ?? 0, message: msg.message ?? "" });
          this.finish("exited", msg.message ?? "");
          break;
        case "error":
          // An error before ready means the target can't be (re)attached:
          // the terminal is gone or the task/service isn't running.
          this.finish(gotReady ? "exited" : "ended", msg.message ?? "session error");
          break;
      }
    };

    ws.onerror = () => {
      // onclose follows and drives reconnection.
    };

    ws.onclose = () => {
      if (this.done) return;
      this.ws = null;
      if (this.opts.reconnect === false) {
        this.finish("ended", "connection closed");
        return;
      }
      this.attempt++;
      this.setPhase("reconnecting", "connection lost");
      this.retryTimer = setTimeout(() => {
        if (!this.done) this.connect();
      }, backoffDelay(this.attempt, this.opts.backoff));
    };
  }
}
