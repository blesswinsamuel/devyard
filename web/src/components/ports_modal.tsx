import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { Check, Copy, ExternalLink, Globe, RefreshCw, Search, X } from "lucide-solid";
import { ports, showPortsModal, setShowPortsModal, fetchPorts, selectedProject } from "~/store";
import type { PortBinding } from "~/types";
import { Button } from "~/components/ui/button";
import { Badge } from "~/components/ui/badge";

export function PortsModal() {
  const [filter, setFilter] = createSignal("");
  const [copiedPort, setCopiedPort] = createSignal<string | null>(null);

  // Periodic auto-refresh when modal is open
  onMount(() => {
    const timer = setInterval(() => {
      if (showPortsModal()) {
        fetchPorts(selectedProject() ?? undefined);
      }
    }, 3000);
    onCleanup(() => clearInterval(timer));
  });

  const activeProjectPorts = createMemo<PortBinding[]>(() => {
    const proj = selectedProject();
    const allMap = ports();
    if (proj && allMap[proj]) {
      return allMap[proj];
    }
    // Flatten all projects if no specific project is selected or if project list is empty
    return Object.values(allMap).flat();
  });

  const filteredPorts = createMemo(() => {
    const q = filter().toLowerCase().trim();
    const list = activeProjectPorts();
    if (!q) return list;
    return list.filter(
      (p) =>
        p.service.toLowerCase().includes(q) ||
        p.project.toLowerCase().includes(q) ||
        p.port.toString().includes(q) ||
        p.ip.toLowerCase().includes(q) ||
        p.protocol.toLowerCase().includes(q)
    );
  });

  const makeUrl = (binding: PortBinding) => {
    const host = binding.ip === "0.0.0.0" || binding.ip === "::" || binding.ip === "*" ? "localhost" : binding.ip;
    return `http://${host}:${binding.port}`;
  };

  const handleCopy = (binding: PortBinding) => {
    const url = makeUrl(binding);
    navigator.clipboard.writeText(url);
    const key = `${binding.project}:${binding.service}:${binding.port}`;
    setCopiedPort(key);
    setTimeout(() => {
      setCopiedPort(null);
    }, 2000);
  };

  return (
    <Show when={showPortsModal()}>
      <div
        class="fixed inset-0 z-[90] flex items-center justify-center bg-background/70 p-4 backdrop-blur-[2px]"
        onClick={() => setShowPortsModal(false)}
        onKeyDown={(e) => {
          if (e.key === "Escape") setShowPortsModal(false);
        }}
        role="presentation"
      >
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Open Ports"
          class="w-full max-w-2xl border border-border bg-popover p-6 shadow-xl rounded-xl"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Modal Header */}
          <div class="mb-5 flex items-center justify-between gap-3 border-b border-border/60 pb-3">
            <div class="flex items-center gap-2">
              <div class="flex size-8 items-center justify-center rounded-md bg-primary/10 text-primary">
                <Globe class="size-4" />
              </div>
              <div>
                <div class="flex items-center gap-2">
                  <h2 class="text-base font-semibold tracking-tight">Open Ports</h2>
                  <Badge variant="secondary" class="font-mono text-xs">
                    {activeProjectPorts().length} active
                  </Badge>
                </div>
                <p class="text-xs text-muted-foreground">
                  Bound listening sockets for running services in{" "}
                  <span class="font-medium text-foreground">{selectedProject() || "all projects"}</span>
                </p>
              </div>
            </div>
            <div class="flex items-center gap-1.5">
              <Button
                variant="outline"
                size="icon"
                class="size-8"
                onClick={() => fetchPorts(selectedProject() ?? undefined)}
                title="Refresh ports"
              >
                <RefreshCw class="size-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                class="size-8"
                onClick={() => setShowPortsModal(false)}
                title="Close"
              >
                <X class="size-4" />
              </Button>
            </div>
          </div>

          {/* Search Filter */}
          <Show when={activeProjectPorts().length > 0}>
            <div class="relative mb-4">
              <Search class="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                placeholder="Filter by service, port, or IP..."
                value={filter()}
                onInput={(e) => setFilter(e.currentTarget.value)}
                class="w-full rounded-md border border-input bg-background pl-9 pr-3 py-1.5 text-xs ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
          </Show>

          {/* Ports Table / Empty State */}
          <div class="max-h-[350px] overflow-y-auto rounded-lg border border-border/80 bg-background">
            <Show
              when={filteredPorts().length > 0}
              fallback={
                <div class="flex flex-col items-center justify-center py-10 px-4 text-center text-muted-foreground">
                  <Globe class="size-8 stroke-[1.5] mb-2 opacity-50" />
                  <p class="text-sm font-medium">No open ports found</p>
                  <p class="text-xs text-muted-foreground mt-1 max-w-sm">
                    {activeProjectPorts().length === 0
                      ? "None of the currently running services are listening on TCP/UDP ports."
                      : "No ports match your search filter."}
                  </p>
                </div>
              }
            >
              <table class="w-full text-left text-xs">
                <thead class="sticky top-0 bg-muted/60 backdrop-blur-sm border-b border-border/60 text-muted-foreground font-medium">
                  <tr>
                    <th class="px-3.5 py-2">Service</th>
                    <th class="px-3.5 py-2">Bound IP</th>
                    <th class="px-3.5 py-2">Port</th>
                    <th class="px-3.5 py-2">Proto</th>
                    <th class="px-3.5 py-2 text-right">Quick Access</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border/40 font-mono">
                  <For each={filteredPorts()}>
                    {(item) => {
                      const copyKey = `${item.project}:${item.service}:${item.port}`;
                      const isCopied = () => copiedPort() === copyKey;
                      const url = makeUrl(item);

                      return (
                        <tr class="hover:bg-muted/30 transition-colors">
                          <td class="px-3.5 py-2.5 font-sans font-medium text-foreground">
                            <div class="flex items-center gap-1.5">
                              <span>{item.service || item.project}</span>
                              <Show when={!selectedProject() && item.project}>
                                <span class="text-[10px] text-muted-foreground font-mono">
                                  ({item.project})
                                </span>
                              </Show>
                            </div>
                          </td>
                          <td class="px-3.5 py-2.5 text-muted-foreground">{item.ip}</td>
                          <td class="px-3.5 py-2.5 font-semibold text-primary">{item.port}</td>
                          <td class="px-3.5 py-2.5">
                            <Badge variant="outline" class="uppercase text-[10px] px-1.5 py-0">
                              {item.protocol}
                            </Badge>
                          </td>
                          <td class="px-3.5 py-2.5 text-right font-sans">
                            <div class="flex items-center justify-end gap-1.5">
                              <Button
                                variant="ghost"
                                size="sm"
                                class="h-7 px-2 text-xs gap-1 text-muted-foreground hover:text-foreground"
                                onClick={() => handleCopy(item)}
                                title="Copy URL"
                              >
                                <Show when={isCopied()} fallback={<Copy class="size-3" />}>
                                  <Check class="size-3 text-emerald-500" />
                                </Show>
                                <span>{isCopied() ? "Copied" : "Copy"}</span>
                              </Button>
                              <a
                                href={url}
                                target="_blank"
                                rel="noreferrer"
                                class="inline-flex items-center gap-1 h-7 px-2.5 rounded-md bg-primary/10 hover:bg-primary/20 text-primary text-xs font-medium transition-colors"
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

          <div class="mt-4 flex items-center justify-between text-xs text-muted-foreground">
            <span>Ports refresh automatically while open.</span>
            <Button variant="secondary" size="sm" onClick={() => setShowPortsModal(false)}>
              Close
            </Button>
          </div>
        </div>
      </div>
    </Show>
  );
}
