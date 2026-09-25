import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { QueryClient } from "@tanstack/solid-query";
import { DaemonService } from "~/gen/devyard/v1/control_connect";

export const transport = createConnectTransport({
  baseUrl: window.location.origin,
});

export const rpcClient = createPromiseClient(DaemonService, transport);

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 2000,
    },
  },
});
