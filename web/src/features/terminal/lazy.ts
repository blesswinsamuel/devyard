import { lazy } from "solid-js";

/** xterm is only needed for interactive sessions; keep it out of the main chunk. */
export const SessionView = lazy(() => import("./session-view").then((m) => ({ default: m.SessionView })));
