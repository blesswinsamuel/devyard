import { describe, expect, it, vi } from "vitest";
import { AttachSession, attachUrl, type SessionPhase } from "./sessions";

class MockSocket {
  static instances: MockSocket[] = [];
  readyState = 0;
  binaryType = "blob";
  sent: (string | Uint8Array)[] = [];
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  closed = false;
  constructor(readonly url: string) {
    MockSocket.instances.push(this);
  }
  send(data: string | Uint8Array) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
    this.readyState = 3;
  }
  // server side helpers
  open() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  text(msg: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(msg) }));
  }
  binary(bytes: number[]) {
    this.onmessage?.(new MessageEvent("message", { data: new Uint8Array(bytes).buffer }));
  }
  drop() {
    this.readyState = 3;
    this.onclose?.(new CloseEvent("close"));
  }
  json(i: number) {
    return JSON.parse(this.sent[i] as string);
  }
}

function setup(target: ConstructorParameters<typeof AttachSession>[0], reconnect = true) {
  MockSocket.instances = [];
  const phases: SessionPhase[] = [];
  const output: number[][] = [];
  const readies: { sessionId: string; tty: boolean; stdin: boolean }[] = [];
  const session = new AttachSession(
    target,
    { cols: 80, rows: 24 },
    {
      onPhase: (p) => phases.push(p),
      onOutput: (d) => output.push([...d]),
      onReady: (r) => readies.push(r),
    },
    {
      url: "ws://test/ws/attach",
      createSocket: (url) => new MockSocket(url) as never,
      reconnect,
      backoff: { baseMs: 100, maxMs: 100, random: () => 0 },
    },
  );
  return { session, phases, output, readies, ws: () => MockSocket.instances[MockSocket.instances.length - 1]! };
}

describe("AttachSession", () => {
  it("builds ws/wss URLs from the page origin", () => {
    expect(attachUrl({ protocol: "http:", host: "localhost:9090" })).toBe("ws://localhost:9090/ws/attach");
    expect(attachUrl({ protocol: "https:", host: "devyard.test" })).toBe("wss://devyard.test/ws/attach");
  });

  it("sends open, handles ready/output/input/resize", () => {
    const { session, phases, output, readies, ws } = setup({ kind: "terminal", project: "web" });
    const sock = ws();
    expect(sock.binaryType).toBe("arraybuffer");
    sock.open();
    expect(sock.json(0)).toEqual({
      type: "open",
      target: { kind: "terminal", project: "web", name: "", session_id: "" },
      cols: 80,
      rows: 24,
    });
    session.input("early"); // queued until ready
    expect(sock.sent).toHaveLength(1);
    sock.text({ type: "ready", session_id: "s1", tty: true, stdin: true });
    expect(readies).toEqual([{ sessionId: "s1", tty: true, stdin: true }]);
    expect(phases).toContain("ready");
    expect(new TextDecoder().decode(sock.sent[1] as Uint8Array)).toBe("early");
    session.input("ls\r");
    expect(ArrayBuffer.isView(sock.sent[2])).toBe(true);
    sock.binary([104, 105]);
    expect(output).toEqual([[104, 105]]);
    session.resize(100, 30);
    expect(sock.json(3)).toEqual({ type: "resize", cols: 100, rows: 30 });
    session.resize(100, 30); // unchanged: no frame
    expect(sock.sent).toHaveLength(4);
  });

  it("reattaches a terminal by session id after an unexpected drop", () => {
    vi.useFakeTimers();
    const { phases, readies, ws } = setup({ kind: "terminal", project: "web" });
    ws().open();
    ws().text({ type: "ready", session_id: "s1", tty: true });
    ws().drop();
    expect(phases.at(-1)).toBe("reconnecting");
    vi.advanceTimersByTime(100);
    const second = ws();
    expect(MockSocket.instances).toHaveLength(2);
    second.open();
    expect(second.json(0).target.session_id).toBe("s1");
    second.text({ type: "ready", session_id: "s1", tty: true });
    expect(readies).toHaveLength(2);
    vi.useRealTimers();
  });

  it("reports an error before ready as an ended session (no reconnect)", () => {
    vi.useFakeTimers();
    const { phases, ws } = setup({ kind: "terminal", project: "web", sessionId: "gone" });
    ws().open();
    ws().text({ type: "error", message: "session not found" });
    expect(phases.at(-1)).toBe("ended");
    expect(ws().closed).toBe(true);
    vi.advanceTimersByTime(1000);
    expect(MockSocket.instances).toHaveLength(1);
    vi.useRealTimers();
  });

  it("reports exit and stops", () => {
    const exits: unknown[] = [];
    MockSocket.instances = [];
    const s = new AttachSession(
      { kind: "task", project: "web", name: "migrate" },
      { cols: 80, rows: 24 },
      { onExit: (e) => exits.push(e) },
      { url: "ws://x", createSocket: (u) => new MockSocket(u) as never },
    );
    const sock = MockSocket.instances[0]!;
    sock.open();
    expect(sock.json(0).target).toEqual({ kind: "task", project: "web", name: "migrate", session_id: "" });
    sock.text({ type: "ready", session_id: "t1", tty: false, stdin: true });
    expect(s.tty).toBe(false);
    expect(s.stdin).toBe(true);
    s.eof();
    expect(sock.json(1)).toEqual({ type: "eof" });
    sock.text({ type: "exit", exit_code: 3, message: "exit status 3" });
    expect(exits).toEqual([{ exitCode: 3, message: "exit status 3" }]);
    expect(s.phase).toBe("exited");
  });

  it("terminate sends close for terminals; detach just closes", () => {
    const a = setup({ kind: "terminal", project: "web" });
    a.ws().open();
    a.ws().text({ type: "ready", session_id: "s1", tty: true });
    a.session.terminate();
    expect(a.ws().json(1)).toEqual({ type: "close" });
    expect(a.ws().closed).toBe(true);
    expect(a.phases.at(-1)).toBe("closed");

    const b = setup({ kind: "task", project: "web", name: "t" });
    b.ws().open();
    b.ws().text({ type: "ready", session_id: "x", tty: true });
    b.session.terminate();
    expect(b.ws().sent).toHaveLength(1);
    expect(b.ws().closed).toBe(true);
  });

  it("treats omitted booleans in ready as false", () => {
    const { session, ws } = setup({ kind: "service", project: "web", name: "api" });
    ws().open();
    ws().text({ type: "ready", session_id: "x" });
    expect(session.tty).toBe(false);
    expect(session.stdin).toBe(false);
  });

  it("does not reconnect after detach", () => {
    vi.useFakeTimers();
    const { session, ws } = setup({ kind: "service", project: "web", name: "api" });
    ws().open();
    session.detach();
    ws().drop();
    vi.advanceTimersByTime(1000);
    expect(MockSocket.instances).toHaveLength(1);
    vi.useRealTimers();
  });
});
