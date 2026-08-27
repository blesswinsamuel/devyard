import { Show, createEffect, onCleanup } from "solid-js";
import { Loader2, RefreshCw, Server } from "lucide-solid";
import { daemonInfo, fetchDaemonStatus, restartDaemon } from "~/stores/data";
import { setShowDaemonModal, showDaemonModal } from "~/stores/app";
import { wsStatus } from "~/lib/ws";
import { createClock, formatBytes, formatUptime } from "~/lib/format";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";

function Row(props: { label: string; children: import("solid-js").JSX.Element }) {
  return (
    <div class="flex items-center justify-between border-b border-border/60 py-2 last:border-0">
      <span class="text-muted-foreground">{props.label}</span>
      <span class="font-mono text-xs">{props.children}</span>
    </div>
  );
}

export function DaemonStatusModal() {
  const clock = createClock();

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
          <span class="capitalize">{wsStatus()}</span>
          <span
            classList={{
              "ml-auto inline-block size-2 rounded-full": true,
              "bg-success shadow-[0_0_6px_var(--success)]": wsStatus() === "open",
              "bg-warning animate-pulse": wsStatus() === "connecting",
              "bg-destructive": wsStatus() === "closed",
            }}
          />
        </div>

        <Show when={daemonInfo()}>
          {(info) => (
            <div class="text-[13px]">
              <Row label="Process ID">{info().pid}</Row>
              <Row label="Uptime" >{formatUptime(info().start_time, clock.now())}</Row>
              <Row label="Goroutines">{info().goroutines}</Row>
              <Row label="Memory (alloc / RSS)">
                {formatBytes(info().memory_alloc)} / {formatBytes(info().memory_rss)}
              </Row>
              <Row label="Go version">{info().go_version}</Row>
            </div>
          )}
        </Show>

        <div class="flex items-center justify-between gap-2">
          <Button variant="ghost" size="sm" onClick={() => fetchDaemonStatus()}>
            <RefreshCw class="size-3.5" />
            Refresh
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => restartDaemon()}
          >
            Restart daemon
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
