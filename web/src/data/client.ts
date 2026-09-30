import { createPromiseClient, type PromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { DaemonService } from "~/gen/devyard/v1/control_connect";

export type DaemonClient = PromiseClient<typeof DaemonService>;

/** Same-origin ConnectRPC client for the daemon. */
export const api: DaemonClient = createPromiseClient(
  DaemonService,
  createConnectTransport({
    baseUrl: typeof location !== "undefined" ? location.origin : "http://localhost",
  }),
);
