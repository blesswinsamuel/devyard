import { api } from "./client";
import { createWatchConnection } from "./connection";
import { entities } from "./entities";
import { isUnauthenticated } from "./errors";
import { queryClient } from "./queries";

/** The app's single Watch connection feeding the entity store. */
export const connection = createWatchConnection({
  open: (signal) => api.watch({}, { signal }),
  onMessage: (msg) => entities.apply(msg),
  onLive: (reconnected) => {
    // The daemon may have restarted or config changed while we were away.
    if (reconnected) void queryClient.invalidateQueries();
  },
  onError: (err) => {
    // A password was set or changed elsewhere: this device has no session
    // anymore. Reload to re-probe (the login screen takes over).
    if (isUnauthenticated(err)) window.location.reload();
  },
});

export const isStale = () => connection.state.phase !== "live" && connection.state.everConnected;
