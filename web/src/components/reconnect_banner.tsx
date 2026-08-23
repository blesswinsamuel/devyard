import { Show } from "solid-js";
import { Loader2 } from "lucide-solid";
import { reconnectAttempt, wsStatus } from "~/lib/ws";

/**
 * Fixed banner shown while the daemon connection is down, so stale data is
 * never mistaken for live data.
 */
export function ReconnectBanner() {
  return (
    <Show when={wsStatus() !== "open"}>
      <div class="pointer-events-none fixed left-1/2 top-3 z-[110] -translate-x-1/2">
        <div class="flex items-center gap-2 rounded-full border border-warning/50 bg-popover/95 px-3.5 py-1.5 text-xs font-medium text-foreground shadow-md backdrop-blur">
          <Loader2 class="size-3.5 animate-spin text-warning" />
          <Show when={wsStatus() === "connecting"} fallback={<span>Daemon disconnected</span>}>
            <span>
              Connecting to daemon
              <Show when={reconnectAttempt() > 0}>
                <span class="text-muted-foreground"> · attempt {reconnectAttempt()}</span>
              </Show>
            </span>
          </Show>
        </div>
      </div>
    </Show>
  );
}
