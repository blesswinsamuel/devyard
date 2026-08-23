/**
 * Headless smoke test: boots the real store stack against a fake WebSocket
 * and asserts that server responses actually reach the reactive state.
 * Catches "handler never registered" regressions that tsc cannot see.
 *
 * Run: bun run scripts/store-smoke.ts
 */
import assert from "node:assert";

// --- browser-ish globals before app imports -------------------------------

const storage = new Map<string, string>();
(globalThis as Record<string, unknown>).localStorage = {
  getItem: (k: string) => storage.get(k) ?? null,
  setItem: (k: string, v: string) => void storage.set(k, String(v)),
  removeItem: (k: string) => void storage.delete(k),
};
(globalThis as Record<string, unknown>).document = {
  documentElement: { classList: { toggle: () => {} } },
};
(globalThis as Record<string, unknown>).location = {
  protocol: "http:",
  host: "smoke.test",
};
(globalThis as Record<string, unknown>).window = {
  location: { pathname: "/", href: "http://smoke.test/" },
  history: { pushState: () => {}, replaceState: () => {} },
  addEventListener: () => {},
  removeEventListener: () => {},
};
(globalThis as Record<string, unknown>).history = {
  pushState: () => {},
  replaceState: () => {},
};

let activeSocket: FakeWebSocket | null = null;

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSING = 2;
  readyState = FakeWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;

  constructor(public url: string) {
    activeSocket = this;
    queueMicrotask(() => this.open());
  }
  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.readyState = FakeWebSocket.CLOSING;
    this.onclose?.();
  }
  /** Simulate one JSON frame from the server. */
  receive(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
}

(globalThis as Record<string, unknown>).WebSocket = FakeWebSocket;

// --- exercise the real stack ----------------------------------------------

const tick = () => new Promise((r) => setTimeout(r, 5));

async function main() {
  const { start } = await import("~/stores/nav");
  const data = await import("~/stores/data");

  const stop = start();
  await tick();

  // start() should have connected and queued refreshes.
  const sock = activeSocket;
  assert.ok(sock, "websocket created");
  await tick();
  const types = sock.sent.map((d) => JSON.parse(d).type);
  assert.ok(types.includes("list_projects"), `expected list_projects request, got ${types}`);

  // Server answers with one project.
  sock.receive({
    type: "projects",
    data: [{ name: "demo", status: "running", config_path: "/tmp/demo/local-compose.yml" }],
  });

  // The response MUST land in the reactive store (regression guard for
  // unregistered handlers).
  assert.equal(data.projects().length, 1, "projects signal populated");
  assert.equal(data.projects()[0]?.name, "demo");

  // Service snapshot flows through too.
  sock.receive({
    type: "services",
    project: "demo",
    data: [{ name: "web", status: "running", pid: 42, exit_code: 0, restarts: 0, has_health: false, health: "" }],
  });
  assert.equal((data.services()["demo"] ?? []).length, 1, "services signal populated");

  // Log subscription requests are issued when asked.
  const { subscribeLogs } = await import("~/lib/ws");
  const unsubscribe = subscribeLogs("demo", "web", () => {});
  assert.ok(
    sock.sent.some((d) => JSON.parse(d).type === "subscribe_logs"),
    "subscribe_logs issued"
  );
  // And streamed lines are routed back to the subscriber.
  let gotLine = "";
  unsubscribe();
  subscribeLogs("demo", "web", (line) => (gotLine = line));
  sock.receive({ type: "log_line", project: "demo", service: "web", line: "hello" });
  assert.equal(gotLine, "hello", "log line routed to subscriber");

  // A global ports snapshot is distributed into per-project buckets so
  // sidebar chips and badges light up from the periodic refresh.
  sock.receive({
    type: "ports",
    data: [
      { project: "demo", service: "web", pid: 42, ip: "127.0.0.1", port: 3000, protocol: "tcp" },
      { project: "other", service: "api", pid: 7, ip: "0.0.0.0", port: 8080, protocol: "tcp" },
    ],
  });
  const portsMap = data.ports();
  assert.equal(portsMap[""]?.length, 2, "global snapshot cached under ''");
  assert.equal(portsMap["demo"]?.length, 1, "per-project bucket populated");
  assert.equal(portsMap["other"]?.length, 1, "other-project bucket populated");
  // An explicit per-project fetch still lands under its own key.
  sock.receive({
    type: "ports",
    project: "solo",
    data: [{ project: "solo", service: "db", pid: 9, ip: "::1", port: 5432, protocol: "tcp" }],
  });
  assert.equal(data.ports()["solo"]?.length, 1, "scoped fetch stored by project");

  stop();
  console.log("store-smoke: OK — handlers wired, signals update, streams route");
}

main().catch((e) => {
  console.error("store-smoke FAILED:", e?.message ?? e);
  process.exit(1);
});
