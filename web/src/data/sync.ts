import { api } from "./client";
import { createWatchConnection } from "./connection";
import { entities } from "./entities";
import { queryClient } from "./queries";

/** The app's single Watch connection feeding the entity store. */
export const connection = createWatchConnection({
  open: (signal) => api.watch({}, { signal }),
  onMessage: (msg) => entities.apply(msg),
  onLive: (reconnected) => {
    // The daemon may have restarted or config changed while we were away.
    if (reconnected) void queryClient.invalidateQueries();
  },
});

export const isStale = () => connection.state.phase !== "live" && connection.state.everConnected;
