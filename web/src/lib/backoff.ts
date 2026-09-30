export interface BackoffOptions {
  baseMs: number;
  maxMs: number;
  /** Returns a value in [0, 1). Injectable for tests. */
  random?: () => number;
}

export const DEFAULT_BACKOFF: BackoffOptions = { baseMs: 500, maxMs: 8000 };

/**
 * Exponential backoff with "equal jitter": the delay for attempt n (1-based)
 * is uniformly distributed in [d/2, d) where d = min(max, base * 2^(n-1)).
 * Keeps retries spread out without ever retrying instantly.
 */
export function backoffDelay(attempt: number, opts: BackoffOptions = DEFAULT_BACKOFF): number {
  const random = opts.random ?? Math.random;
  const exp = Math.min(opts.maxMs, opts.baseMs * 2 ** Math.max(0, attempt - 1));
  return Math.round(exp / 2 + random() * (exp / 2));
}

/** Resolves after `ms`, or early (without rejecting) when the signal aborts. */
export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal?.aborted) return resolve();
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener("abort", done);
      resolve();
    }
    signal?.addEventListener("abort", done, { once: true });
  });
}
