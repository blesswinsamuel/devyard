import { afterEach, describe, expect, it, vi } from "vitest";
import type { WatchResponse } from "~/gen/devyard/v1/control_pb";
import { createWatchConnection } from "./connection";
import { heartbeat, snapshot } from "~/test/fixtures";

/** A controllable server stream. */
function controlledStream() {
  const queue: WatchResponse[] = [];
  let wake: (() => void) | null = null;
  let failure: Error | null = null;
  let ended = false;
  const iterable: AsyncIterable<WatchResponse> = {
    async *[Symbol.asyncIterator]() {
      for (;;) {
        while (queue.length) yield queue.shift()!;
        if (failure) throw failure;
        if (ended) return;
        await new Promise<void>((r) => (wake = r));
      }
    },
  };
  const poke = () => {
    const w = wake;
    wake = null;
    w?.();
  };
  return {
    iterable,
    push(m: WatchResponse) {
      queue.push(m);
      poke();
    },
    fail(err: Error) {
      failure = err;
      poke();
    },
    end() {
      ended = true;
      poke();
    },
  };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("watch connection", () => {
  afterEach(() => vi.useRealTimers());

  it("goes live on the first message and reconnects with growing jittered backoff", async () => {
    const streams: ReturnType<typeof controlledStream>[] = [];
    const delays: number[] = [];
    const sleeps: (() => void)[] = [];
    const messages: WatchResponse[] = [];
    const lives: boolean[] = [];
    const conn = createWatchConnection({
      open: () => {
        const s = controlledStream();
        streams.push(s);
        return s.iterable;
      },
      onMessage: (m) => messages.push(m),
      onLive: (reconnected) => lives.push(reconnected),
      backoff: { baseMs: 500, maxMs: 8000, random: () => 0.5 },
      sleep: (ms) => {
        delays.push(ms);
        return new Promise<void>((r) => sleeps.push(r));
      },
      now: () => 1000,
    });
    conn.start();
    await flush();
    expect(conn.state.phase).toBe("connecting");

    streams[0]!.push(snapshot(1, {}));
    await flush();
    expect(conn.state.phase).toBe("live");
    expect(lives).toEqual([false]);

    streams[0]!.fail(new Error("boom"));
    await flush();
    expect(conn.state.phase).toBe("reconnecting");
    expect(conn.state.attempt).toBe(1);
    expect(conn.state.lastError).toBe("boom");
    expect(conn.state.retryAt).toBe(1000 + 375);

    // Failing attempts back off exponentially: 375, 750, 1500, … (0.75 × d).
    for (let i = 0; i < 5; i++) {
      sleeps.shift()!();
      await flush();
      streams[streams.length - 1]!.fail(new Error("down"));
      await flush();
    }
    expect(delays).toEqual([375, 750, 1500, 3000, 6000, 6000]);
    expect(conn.state.attempt).toBe(6);

    // Recovery resets the attempt counter and reports a reconnect.
    sleeps.shift()!();
    await flush();
    streams[streams.length - 1]!.push(snapshot(1, {}));
    await flush();
    expect(conn.state.phase).toBe("live");
    expect(conn.state.attempt).toBe(0);
    expect(lives).toEqual([false, true]);
    expect(messages).toHaveLength(2);
    conn.dispose();
  });

  it("treats a cleanly ended stream as a disconnect", async () => {
    const streams: ReturnType<typeof controlledStream>[] = [];
    const conn = createWatchConnection({
      open: () => {
        const s = controlledStream();
        streams.push(s);
        return s.iterable;
      },
      onMessage: () => {},
      sleep: () => new Promise(() => {}),
    });
    conn.start();
    streams[0]!.push(snapshot(1, {}));
    await flush();
    streams[0]!.end();
    await flush();
    expect(conn.state.phase).toBe("reconnecting");
    expect(conn.state.lastError).toMatch(/closed/);
    conn.dispose();
  });

  it("aborts a silent stream after the heartbeat timeout", async () => {
    vi.useFakeTimers();
    const signals: AbortSignal[] = [];
    const conn = createWatchConnection({
      open: (signal) => {
        signals.push(signal);
        const s = controlledStream();
        signal.addEventListener("abort", () => s.fail(new Error("aborted")));
        if (signals.length === 1) s.push(snapshot(1, {}));
        return s.iterable;
      },
      onMessage: () => {},
      heartbeatTimeoutMs: 1000,
      sleep: () => new Promise(() => {}),
    });
    conn.start();
    await vi.advanceTimersByTimeAsync(10);
    expect(conn.state.phase).toBe("live");
    await vi.advanceTimersByTimeAsync(1100);
    expect(signals[0]!.aborted).toBe(true);
    expect(conn.state.phase).toBe("reconnecting");
    expect(conn.state.lastError).toBe("no heartbeat from daemon");
    conn.dispose();
  });

  it("heartbeats keep the stream alive", async () => {
    vi.useFakeTimers();
    let stream!: ReturnType<typeof controlledStream>;
    const conn = createWatchConnection({
      open: (signal) => {
        stream = controlledStream();
        signal.addEventListener("abort", () => stream.fail(new Error("aborted")));
        return stream.iterable;
      },
      onMessage: () => {},
      heartbeatTimeoutMs: 1000,
      sleep: () => new Promise(() => {}),
    });
    conn.start();
    stream.push(snapshot(1, {}));
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(800);
      stream.push(heartbeat(1));
    }
    await vi.advanceTimersByTimeAsync(10);
    expect(conn.state.phase).toBe("live");
    conn.dispose();
  });

  it("retryNow skips the backoff wait", async () => {
    let opens = 0;
    const conn = createWatchConnection({
      open: () => {
        opens++;
        const s = controlledStream();
        s.fail(new Error("refused"));
        return s.iterable;
      },
      onMessage: () => {},
      backoff: { baseMs: 60_000, maxMs: 60_000 },
    });
    conn.start();
    await flush();
    expect(opens).toBe(1);
    conn.retryNow();
    await flush();
    await flush();
    expect(opens).toBe(2);
    conn.dispose();
  });
});
