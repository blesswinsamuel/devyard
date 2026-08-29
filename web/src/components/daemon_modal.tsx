import { Show, createEffect, createSignal, onCleanup } from "solid-js";
import { RefreshCw, Server } from "lucide-solid";
import { daemonInfo, fetchDaemonStatus, restartDaemon } from "~/stores/data";
import { setShowDaemonModal, showDaemonModal } from "~/stores/app";
import { eventStatus } from "~/lib/events";
import { createClock, formatBytes, formatUptime } from "~/lib/format";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Separator } from "~/components/ui/separator";

function Row(props: { label: string; children: import("solid-js").JSX.Element; last?: boolean }) {
  return (
    <>
      <div class="flex items-center justify-between py-2">
        <span class="text-muted-foreground">{props.label}</span>
        <span class="font-mono text-xs">{props.children}</span>
      </div>
      <Show when={!props.last}>
        <Separator class="opacity-60" />
      </Show>
    </>
  );
}

export function DaemonStatusModal() {
  const clock = createClock();
  const [restartServices, setRestartServices] = createSignal(false);

  createEffect(() => {
    if (showDaemonModal()) {
      fetchDaemonStatus();
      const timer = setInterval(fetchDaemonStatus, 2000);
      onCleanup(() => clearInterval(timer));
    }
  });
  onCleanup(() => clock.dispose());

  return (
    <Dialog open={showDaemonModal()} onOpenChange={setShowDaemonModal}>
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Daemon Status</DialogTitle>
          <DialogDescription class="sr-only">View and control the local-compose daemon process.</DialogDescription>
        </DialogHeader>
        <div class="flex items-center gap-2 rounded-lg bg-muted/60 px-3 py-2 text-xs">
          <Server class="size-4 text-primary" />
          <span class="capitalize">{eventStatus()}</span>
          <span
            classList={{
              "ml-auto inline-block size-2 rounded-full": true,
              "bg-success shadow-[0_0_6px_var(--success)]": eventStatus() === "open",
              "bg-warning animate-pulse": eventStatus() === "connecting",
              "bg-destructive": eventStatus() === "closed",
            }}
          />
        </div>

        <Show when={daemonInfo()}>
          {(info) => (
            <div class="text-[13px]">
              <Row label="Process ID">{info().pid}</Row>
              <Row label="Uptime">{formatUptime(info().startTime ?? (info() as any).start_time, clock.now())}</Row>
              <Row label="Goroutines">{info().goroutines}</Row>
              <Row label="Memory (alloc / RSS)">
                {formatBytes(info().memoryAlloc ?? (info() as any).memory_alloc)} / {formatBytes(info().memoryRss ?? (info() as any).memory_rss)}
              </Row>
              <Row label="Go version" last>{info().goVersion ?? (info() as any).go_version}</Row>
            </div>
          )}
        </Show>

        <div class="flex items-center justify-between rounded-md border p-2 text-xs">
          <label for="restart-services-switch" class="cursor-pointer text-muted-foreground">
            Restart services too
          </label>
          <input
            id="restart-services-switch"
            type="checkbox"
            checked={restartServices()}
            onChange={(e) => setRestartServices(e.currentTarget.checked)}
            class="size-4 rounded border-input cursor-pointer accent-primary"
          />
        </div>

        <div class="flex items-center justify-between gap-2">
          <Button variant="ghost" size="sm" onClick={() => fetchDaemonStatus()}>
            <RefreshCw class="size-3.5" />
            Refresh
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => restartDaemon(restartServices())}
          >
            Restart daemon
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
