import { Code, ConnectError } from "@connectrpc/connect";

export interface ErrorInfo {
  code: Code | undefined;
  /** Short, code-derived explanation. */
  reason: string;
  /** The server's message (without the "[code]" prefix). */
  message: string;
}

const REASONS: Partial<Record<Code, string>> = {
  [Code.NotFound]: "Not found",
  [Code.FailedPrecondition]: "Not possible in the current state",
  [Code.AlreadyExists]: "Already exists",
  [Code.InvalidArgument]: "Invalid input",
  [Code.Unavailable]: "Daemon unavailable (shutting down or unreachable)",
  [Code.DeadlineExceeded]: "Timed out",
  [Code.PermissionDenied]: "Permission denied",
  [Code.Unauthenticated]: "Not authenticated",
  [Code.Internal]: "Daemon error",
};

export function errorInfo(err: unknown): ErrorInfo {
  if (err instanceof ConnectError || (err instanceof Error && err.name === "ConnectError")) {
    const ce = ConnectError.from(err);
    return { code: ce.code, reason: REASONS[ce.code] ?? Code[ce.code] ?? "Error", message: ce.rawMessage };
  }
  return { code: undefined, reason: "Error", message: errorMessage(err) };
}

export function errorMessage(err: unknown): string {
  if (err instanceof ConnectError) return err.rawMessage;
  if (err instanceof Error) return err.message;
  return String(err);
}

/** Aborted calls (navigation, unmount) are not user-facing errors. */
export function isCanceled(err: unknown): boolean {
  if (err instanceof ConnectError) return err.code === Code.Canceled;
  return err instanceof DOMException && err.name === "AbortError";
}
