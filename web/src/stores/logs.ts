import { rpcClient } from "~/lib/rpc";

// Log stream subscriptions. Tab state lives in `stores/workspace.ts` — these
// helpers only wire a log source to callbacks.

export function subscribeLogs(
  project: string,
  service: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false,
  onError?: (err: unknown) => void
): () => void {
  return subscribeLogStream({ project, service }, onLine, onRotate, prev, onError);
}

export function subscribeTaskLogs(
  project: string,
  task: string,
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false,
  onError?: (err: unknown) => void
): () => void {
  return subscribeLogStream({ project, task }, onLine, onRotate, prev, onError);
}

function subscribeLogStream(
  target: { project: string; service?: string; task?: string },
  onLine: (line: string) => void,
  onRotate?: () => void,
  prev = false,
  onError?: (err: unknown) => void
): () => void {
  const controller = new AbortController();
  (async () => {
    try {
      const stream = rpcClient.logs(
        {
          ...target,
          follow: !prev,
          previous: prev,
          tail: 500,
        },
        { signal: controller.signal }
      );

      for await (const chunk of stream) {
        if (controller.signal.aborted) break;
        if (chunk.rotated) {
          onRotate?.();
        }
        if (chunk.content) {
          const contentLines = chunk.content.split("\n");
          if (contentLines.length > 0 && contentLines[contentLines.length - 1] === "") {
            contentLines.pop();
          }
          for (const line of contentLines) {
            onLine(line);
          }
        }
        for (const line of chunk.lines) {
          onLine(line);
        }
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        onError?.(err);
      }
    }
  })();

  return () => controller.abort();
}
