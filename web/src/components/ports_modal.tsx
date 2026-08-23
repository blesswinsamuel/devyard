import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { Check, Copy, ExternalLink, Globe, RefreshCw, Search } from "lucide-solid";
import { fetchPorts, ports as portsMap } from "~/stores/data";
import { selectedProject } from "~/stores/nav";
import { setShowPortsModal, showPortsModal } from "~/stores/app";
import type { PortBinding } from "~/lib/types";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent } from "~/components/ui/dialog";

function makeUrl(binding: PortBinding): string {
  const host =
    binding.ip === "0.0.0.0" || binding.ip === "::" || binding.ip === "*"
      ? "localhost"
      : binding.ip;
  return `http://${host}:${binding.port}`;
}

export function PortsModal() {
  const [filter, setFilter] = createSignal("");
  const [copiedPort, setCopiedPort] = createSignal<string | null>(null);

  // Auto-refresh while open.
  createEffect(() => {
    if (showPortsModal()) {
      fetchPorts(selectedProject() ?? undefined);
      const timer = setInterval(() => fetchPorts(selectedProject() ?? undefined), 3000);
      onCleanup(() => clearInterval(timer));
    }
  });

  const activePorts = createMemo<PortBinding[]>(() => {
    const proj = selectedProject();
    const allMap = portsMap();
    if (proj && allMap[proj]) return allMap[proj];
    return Object.values(allMap).flat();
  });

  const filteredPorts = createMemo(() => {
    const q = filter().toLowerCase().trim();
    const list = activePorts();
    if (!q) return list;
    return list.filter(
      (p) =>
        p.service.toLowerCase().includes(q) ||
        p.project.toLowerCase().includes(q) ||
        String(p.port).includes(q) ||
        p.ip.toLowerCase().includes(q)
    );
  });

  const handleCopy = (binding: PortBinding) => {
    void navigator.clipboard.writeText(makeUrl(binding));
    const key = `${binding.project}:${binding.service}:${binding.port}`;
    setCopiedPort(key);
    setTimeout(() => setCopiedPort(null), 1500);
  };

  return (
    <Dialog open={showPortsModal()} onOpenChange={setShowPortsModal}>
      <DialogContent title="Open Ports" class="max-w-2xl">
        <div class="px-5 py-4">
          <div class="mb-3 flex items-center justify-between gap-2">
            <div class="flex items-center gap-2 text-xs text-muted-foreground">
              <Globe class="size-3.5" />
              <span>
                Listening sockets for{" "}
                <span class="font-medium text-foreground">{selectedProject() || "all projects"}</span>
              </span>
              <Badge variant="secondary" class="font-mono">
                {activePorts().length}
              </Badge>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => fetchPorts(selectedProject() ?? undefined)}
              title="Refresh"
            >
              <RefreshCw class="size-3.5" />
            </Button>
          </div>

          <Show when={activePorts().length > 0}>
            <div class="relative mb-3">
              <Search class="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                placeholder="Filter by service, port, or IP…"
                value={filter()}
                onInput={(e) => setFilter(e.currentTarget.value)}
                class="h-8 w-full rounded-md border border-input bg-background pl-8 pr-3 text-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
          </Show>

          <div class="max-h-[350px] overflow-y-auto rounded-lg border bg-background">
            <Show
              when={filteredPorts().length > 0}
              fallback={
                <div class="flex flex-col items-center justify-center px-4 py-10 text-center text-muted-foreground">
                  <Globe class="mb-2 size-8 stroke-[1.5] opacity-40" />
                  <p class="text-[13px] font-medium">No open ports found</p>
                  <p class="mt-1 max-w-sm text-xs">
                    {activePorts().length === 0
                      ? "None of the running services are listening on TCP/UDP ports."
                      : "No ports match your filter."}
                  </p>
                </div>
              }
            >
              <table class="w-full text-left text-xs">
                <thead class="sticky top-0 border-b bg-muted/70 text-muted-foreground backdrop-blur-sm">
                  <tr>
                    <th class="px-3.5 py-2 font-medium">Service</th>
                    <th class="px-3.5 py-2 font-medium">Bound IP</th>
                    <th class="px-3.5 py-2 font-medium">Port</th>
                    <th class="px-3.5 py-2 font-medium">Proto</th>
                    <th class="px-3.5 py-2 text-right font-medium">Quick access</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border/60">
                  <For each={filteredPorts()}>
                    {(item) => {
                      const copyKey = `${item.project}:${item.service}:${item.port}`;
                      const isCopied = () => copiedPort() === copyKey;
                      return (
                        <tr class="transition-colors hover:bg-muted/40">
                          <td class="px-3.5 py-2.5 font-medium">
                            <span>{item.service || item.project}</span>
                            <Show when={!selectedProject() && item.project}>
                              <span class="ml-1.5 font-mono text-[10px] text-muted-foreground">
                                ({item.project})
                              </span>
                            </Show>
                          </td>
                          <td data-tabular class="px-3.5 py-2.5 font-mono text-[11px] text-muted-foreground">
                            {item.ip}
                          </td>
                          <td data-tabular class="px-3.5 py-2.5 font-mono font-semibold text-primary">
                            {item.port}
                          </td>
                          <td class="px-3.5 py-2.5">
                            <Badge variant="outline" class="uppercase">
                              {item.protocol}
                            </Badge>
                          </td>
                          <td class="px-3.5 py-2.5">
                            <div class="flex items-center justify-end gap-1.5">
                              <Button
                                variant="ghost"
                                size="xs"
                                class="gap-1 text-muted-foreground hover:text-foreground"
                                onClick={() => handleCopy(item)}
                              >
                                <Show when={isCopied()} fallback={<Copy class="size-3" />}>
                                  <Check class="size-3 text-success" />
                                </Show>
                                {isCopied() ? "Copied" : "Copy"}
                              </Button>
                              <a
                                href={makeUrl(item)}
                                target="_blank"
                                rel="noreferrer"
                                class="inline-flex h-6 items-center gap-1 rounded-md bg-primary/12 px-2 text-xs font-medium text-primary transition-colors hover:bg-primary/20"
                              >
                                Open
                                <ExternalLink class="size-3" />
                              </a>
                            </div>
                          </td>
                        </tr>
                      );
                    }}
                  </For>
                </tbody>
              </table>
            </Show>
          </div>

          <p class="mt-3 text-[11px] text-muted-foreground">
            Ports refresh automatically while this dialog is open.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}
