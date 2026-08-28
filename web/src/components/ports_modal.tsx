import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { Check, Copy, ExternalLink, Globe, RefreshCw, Search } from "lucide-solid";
import { fetchPorts, ports as portsMap } from "~/stores/data";
import { selectedProject } from "~/stores/nav";
import { openPortsModal, portsScope, setShowPortsModal, showPortsModal } from "~/stores/app";
import type { PortBinding } from "~/lib/types";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "~/components/ui/toggle-group";
import { cn } from "~/lib/utils";

function makeUrl(binding: PortBinding): string {
  const host =
    binding.ip === "0.0.0.0" || binding.ip === "::" || binding.ip === "*" || binding.ip === "0.0.0.0"
      ? "localhost"
      : binding.ip;
  return `http://${host}:${binding.port}`;
}

export function PortsModal() {
  const [filter, setFilter] = createSignal("");
  const [copiedPort, setCopiedPort] = createSignal<string | null>(null);

  const scope = () => portsScope();
  const project = () => selectedProject();

  // Fetch on open and auto-refresh while open, honoring the current scope.
  const refresh = () => fetchPorts(scope() === "all" ? undefined : project() ?? undefined);
  createEffect(() => {
    if (showPortsModal()) {
      // Track scope + selected project so switching either refetches.
      scope();
      project();
      refresh();
      const timer = setInterval(refresh, 3000);
      onCleanup(() => clearInterval(timer));
    }
  });

  const activePorts = createMemo<PortBinding[]>(() => {
    const all = portsMap();
    if (scope() === "all") {
      // The global snapshot is cached under ""; fall back to flattening
      // per-project buckets if it hasn't arrived yet.
      if (all[""]) return all[""];
      return Object.entries(all)
        .filter(([key]) => key !== "")
        .flatMap(([, list]) => list);
    }
    return all[project() ?? ""] ?? [];
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

  const setScope = (s: "project" | "all") => openPortsModal(s);

  return (
    <Dialog open={showPortsModal()} onOpenChange={setShowPortsModal}>
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Open Ports</DialogTitle>
          <DialogDescription class="sr-only">Browse open TCP ports across services or projects.</DialogDescription>
        </DialogHeader>
        <div>
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            {/* Scope switch */}
            <ToggleGroup
              variant="outline"
              size="sm"
              spacing={0}
              value={scope()}
              onChange={(v: string | null) => {
                if (v) setScope(v as "project" | "all");
              }}
              class="text-xs"
            >
              <ToggleGroupItem
                value="project"
                disabled={!project()}
                title={!project() ? "No project selected" : `Ports for ${project()}`}
              >
                This project
              </ToggleGroupItem>
              <ToggleGroupItem value="all">All projects</ToggleGroupItem>
            </ToggleGroup>

            <div class="flex items-center gap-2 text-xs text-muted-foreground">
              <Badge variant="secondary" class="font-mono tabular">
                {activePorts().length}
              </Badge>
              <Button variant="ghost" size="icon-sm" onClick={refresh} title="Refresh">
                <RefreshCw class="size-3.5" />
              </Button>
            </div>
          </div>

          <Show when={activePorts().length > 0}>
            <div class="relative mb-3">
              <Search class="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                type="text"
                placeholder="Filter by service, project, port, or IP…"
                value={filter()}
                onInput={(e) => setFilter(e.currentTarget.value)}
                class="h-8 rounded-md bg-background pl-8 pr-3 text-xs md:text-xs"
              />
            </div>
          </Show>

          <div class="max-h-[350px] overflow-y-auto rounded-lg border bg-background">
            <Show
              when={filteredPorts().length > 0}
              fallback={
                <Empty class="py-10">
                  <EmptyHeader>
                    <EmptyMedia>
                      <Globe class="size-8 stroke-[1.5] opacity-40 text-muted-foreground" />
                    </EmptyMedia>
                    <EmptyTitle class="text-[13px]">No open ports</EmptyTitle>
                    <EmptyDescription class="text-xs max-w-sm">
                      {activePorts().length === 0
                        ? scope() === "all"
                          ? "None of the running services are listening on TCP/UDP ports."
                          : `Services in '${project()}' have no listeners right now — try the All projects scope.`
                        : "No ports match your filter."}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              }
            >
              <Table class="w-full text-left text-xs">
                <TableHeader class="sticky top-0 bg-muted/70 text-muted-foreground backdrop-blur-sm">
                  <TableRow class="hover:bg-transparent">
                    <TableHead class="h-8 px-3.5 py-2 font-medium text-muted-foreground">Service</TableHead>
                    <TableHead class="h-8 px-3.5 py-2 font-medium text-muted-foreground">Bound IP</TableHead>
                    <TableHead class="h-8 px-3.5 py-2 font-medium text-muted-foreground">Port</TableHead>
                    <TableHead class="h-8 px-3.5 py-2 font-medium text-muted-foreground">Proto</TableHead>
                    <TableHead class="h-8 px-3.5 py-2 text-right font-medium text-muted-foreground">Quick access</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody class="divide-y divide-border/60">
                  <For each={filteredPorts()}>
                    {(item) => {
                      const copyKey = `${item.project}:${item.service}:${item.port}`;
                      const isCopied = () => copiedPort() === copyKey;
                      return (
                        <TableRow class="transition-colors hover:bg-muted/40">
                          <TableCell class="px-3.5 py-2.5 font-medium">
                            <span>{item.service || item.project}</span>
                            <Show when={scope() === "all" && item.project}>
                              <span class="ml-1.5 rounded bg-muted px-1 py-px font-mono text-[10px] text-muted-foreground">
                                {item.project}
                              </span>
                            </Show>
                          </TableCell>
                          <TableCell data-tabular class="px-3.5 py-2.5 font-mono text-[11px] text-muted-foreground">
                            {item.ip}
                          </TableCell>
                          <TableCell data-tabular class="px-3.5 py-2.5 font-mono font-semibold text-primary">
                            {item.port}
                          </TableCell>
                          <TableCell class="px-3.5 py-2.5">
                            <Badge variant="outline" class="uppercase">
                              {item.protocol}
                            </Badge>
                          </TableCell>
                          <TableCell class="px-3.5 py-2.5">
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
                                class={cn(
                                  "inline-flex h-6 items-center gap-1 rounded-md bg-primary/12 px-2",
                                  "text-xs font-medium text-primary transition-colors hover:bg-primary/20"
                                )}
                              >
                                Open
                                <ExternalLink class="size-3" />
                              </a>
                            </div>
                          </TableCell>
                        </TableRow>
                      );
                    }}
                  </For>
                </TableBody>
              </Table>
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
