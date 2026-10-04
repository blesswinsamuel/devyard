import { Show } from "solid-js";
import { LoaderCircle, TriangleAlert, WifiOff } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { entities } from "~/data/entities";
import { connection } from "~/data/sync";
import { now } from "~/lib/format";

/** The single connection banner (shown while the Watch stream is down). */
export function ReconnectBanner() {
  const s = connection.state;
  const seconds = () => Math.max(0, Math.ceil((s.retryAt - now()) / 1000));
  return (
    <Show when={s.phase === "reconnecting"}>
      <div
        class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-warning/30 bg-warning/10 px-4 py-1.5 text-ui"
        role="status"
        aria-live="polite"
      >
        <Show when={s.everConnected} fallback={<WifiOff class="size-4 text-warning" />}>
          <LoaderCircle class="size-4 animate-spin text-warning" />
        </Show>
        <span class="font-medium">
          {s.everConnected ? "Reconnecting to daemon…" : "Can't reach the devyard daemon."}
        </span>
        <span class="text-muted-foreground">
          <Show when={!s.everConnected}>
            Start it with <code class="font-mono">devyard daemon start</code>.{" "}
          </Show>
          {seconds() > 0 ? `Retrying in ${seconds()}s` : "Retrying…"}
          <Show when={s.lastError}> · {s.lastError}</Show>
        </span>
        <Button size="xs" variant="outline" class="ml-auto" onClick={() => connection.retryNow()}>
          Retry now
        </Button>
      </div>
    </Show>
  );
}

/** Shown while the daemon is running on the last good global config because
 * the file on disk could not be (fully) applied. */
export function ConfigErrorBanner() {
  const error = () => entities.state.daemon?.configError ?? "";
  return (
    <Show when={error()}>
      <div
        class="flex shrink-0 items-start gap-3 border-b border-destructive/30 bg-destructive/10 px-4 py-1.5 text-ui"
        role="alert"
      >
        <TriangleAlert class="mt-0.5 size-4 shrink-0 text-destructive" />
        <div class="min-w-0">
          <span class="font-medium">The global config could not be applied.</span>{" "}
          <span class="text-muted-foreground">The last working settings stay in effect. Fix the file; it is re-read automatically.</span>
          <pre class="mt-0.5 font-mono text-2xs break-words whitespace-pre-wrap">{error()}</pre>
        </div>
      </div>
    </Show>
  );
}
