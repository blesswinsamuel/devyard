import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { Check, Copy, ExternalLink, Globe, RefreshCw, Search } from "lucide-solid";
import { fetchPorts, ports as portsMap, projects as projectsList, services as servicesMap } from "~/stores/data";
import { setShowPortsDialog, showPortsDialog } from "~/stores/app";
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
import { cleanProxyUrl, portDisplay } from "~/lib/format";

type UrlItem = { label: string; href: string };

type ServiceRow = {
  project: string;
  service: string;
  ports: PortBinding[];
  urls: UrlItem[];
};

const REFRESH_INTERVAL_MS = 3000;

function buildServiceRows(project: string, bindings: PortBinding[], proxyUrls: Map<string, string[]>): ServiceRow[] {
  const rows = new Map<string, ServiceRow>();
  const row = (service: string): ServiceRow => {
    let r = rows.get(service);
    if (!r) {
      r = { project, service, ports: [], urls: [] };
      rows.set(service, r);
    }
    return r;
  };
  for (const [service, urls] of proxyUrls) {
    row(service).urls.push(...urls.map((url) => ({ label: cleanProxyUrl(url), href: url })));
  }
  for (const b of bindings) {
    row(b.service || project).ports.push(b);
  }
  return [...rows.values()];
}

/**
 * Ports & web URLs for one project (project set) or every project (project
 * undefined). Polls listPorts while mounted.
 */
export function PortsPanel(props: { project?: string }) {
  const [filter, setFilter] = createSignal("");
  const [copiedHref, setCopiedHref] = createSignal<string | null>(null);

  createEffect(() => {
    // Track the project so switching refetches, then poll while mounted.
    const project = props.project;
    fetchPorts(project);
    const timer = setInterval(() => fetchPorts(project), REFRESH_INTERVAL_MS);
    onCleanup(() => clearInterval(timer));
  });

  const projectNames = createMemo(() => {
    if (props.project !== undefined) return [props.project];
    const names = new Set<string>(projectsList().map((p) => p.name));
    for (const key of Object.keys(portsMap())) {
      if (key) names.add(key);
    }
    return [...names];
  });

  const rows = createMemo((): ServiceRow[] => {
    const q = filter().toLowerCase().trim();
    const all = projectNames().flatMap((project) => {
      const proxyUrls = new Map<string, string[]>();
      for (const s of servicesMap()[project] ?? []) {
        if (s.proxyUrls?.length) proxyUrls.set(s.name, s.proxyUrls);
      }
      return buildServiceRows(project, portsMap()[project] ?? [], proxyUrls);
    });
    if (!q) return all;
    return all.filter(
      (r) =>
        r.service.toLowerCase().includes(q) ||
        r.project.toLowerCase().includes(q) ||
        r.ports.some((p) => String(p.port).includes(q) || p.ip.toLowerCase().includes(q)) ||
        r.urls.some((u) => u.label.toLowerCase().includes(q) || u.href.toLowerCase().includes(q))
    );
  });

  const copy = (href: string) => {
    void navigator.clipboard.writeText(href);
    setCopiedHref(href);
    setTimeout(() => setCopiedHref(null), 1500);
  };

  return (
    <div>
      <Show when={rows().length > 0}>
        <div class="mb-3 flex items-center gap-2">
          <div class="relative flex-1">
            <Search class="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              type="text"
              placeholder="Filter by service, port, or URL…"
              value={filter()}
              onInput={(e) => setFilter(e.currentTarget.value)}
              class="h-8 rounded-md bg-background pl-8 pr-3 text-xs md:text-xs"
            />
          </div>
          <Badge variant="secondary" class="font-mono tabular">
            {rows().length}
          </Badge>
          <Button variant="ghost" size="icon-sm" onClick={() => fetchPorts(props.project)} title="Refresh">
            <RefreshCw class="size-3.5" />
          </Button>
        </div>
      </Show>

      <div class="max-h-[420px] overflow-auto rounded-lg border bg-background">
        <Show
          when={rows().length > 0}
          fallback={
            <Empty class="py-10">
              <EmptyHeader>
                <EmptyMedia>
                  <Globe class="size-8 stroke-[1.5] opacity-40 text-muted-foreground" />
                </EmptyMedia>
                <EmptyTitle class="text-[13px]">
                  {filter() ? "No matches" : props.project ? "No open ports or web URLs" : "No open ports"}
                </EmptyTitle>
                <EmptyDescription class="text-xs max-w-sm">
                  {filter()
                    ? "Nothing matches your filter."
                    : props.project
                      ? "Listening ports and proxy URLs for this project's services show up here."
                      : "None of the running services are listening on ports."}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          }
        >
          <Table class="text-xs">
            <TableHeader class="sticky top-0 z-10 bg-muted/70 backdrop-blur-sm">
              <TableRow class="hover:bg-transparent">
                <TableHead class="h-8 px-3.5 text-muted-foreground">Service</TableHead>
                <TableHead class="h-8 px-3.5 text-muted-foreground">Ports</TableHead>
                <TableHead class="h-8 px-3.5 text-right text-muted-foreground">Web URLs</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody class="divide-y divide-border/60">
              <For each={rows()}>
                {(row) => (
                  <TableRow class="hover:bg-muted/40">
                    <TableCell class="px-3.5 py-2 align-middle">
                      <Show when={props.project === undefined} fallback={<span>{row.service}</span>}>
                        <div class="flex flex-col">
                          <span class="font-semibold">{row.project}</span>
                          <span class="text-muted-foreground">{row.service}</span>
                        </div>
                      </Show>
                    </TableCell>
                    <TableCell class="px-3.5 py-2">
                      <Show when={row.ports.length > 0} fallback={<span class="text-muted-foreground">—</span>}>
                        <div class="flex flex-wrap gap-1">
                          <For each={row.ports}>
                            {(b) => (
                              <span
                                data-tabular
                                class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
                              >
                                {portDisplay(b)}
                                <span class="ml-1 uppercase opacity-70">{b.protocol}</span>
                              </span>
                            )}
                          </For>
                        </div>
                      </Show>
                    </TableCell>
                    <TableCell class="px-3.5 py-2">
                      <div class="flex flex-wrap items-center justify-end gap-1">
                        <For each={row.urls}>
                          {(url) => (
                            <span class="inline-flex items-center gap-0.5">
                              <a
                                href={url.href}
                                target="_blank"
                                rel="noreferrer"
                                data-tabular
                                class="inline-flex h-6 items-center rounded-md bg-primary/12 px-2 font-mono text-[11px] font-medium text-primary transition-colors hover:bg-primary/20"
                              >
                                {url.label}
                                <ExternalLink class="ml-1 size-3" />
                              </a>
                              <Button
                                variant="ghost"
                                size="icon-xs"
                                class="text-muted-foreground"
                                onClick={() => copy(url.href)}
                                title="Copy URL"
                              >
                                <Show when={copiedHref() === url.href} fallback={<Copy class="size-3" />}>
                                  <Check class="size-3 text-success" />
                                </Show>
                              </Button>
                            </span>
                          )}
                        </For>
                      </div>
                    </TableCell>
                  </TableRow>
                )}
              </For>
            </TableBody>
          </Table>
        </Show>
      </div>
    </div>
  );
}

/** Global ports & URLs browser, opened from the sidebar. */
export function PortsDialog() {
  return (
    <Dialog open={showPortsDialog()} onOpenChange={setShowPortsDialog}>
      <DialogContent class="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>Ports & URLs</DialogTitle>
          <DialogDescription class="sr-only">
            Listening ports and web URLs across all projects.
          </DialogDescription>
        </DialogHeader>
        <PortsPanel />
        <p class="text-[11px] text-muted-foreground">Ports refresh automatically while this dialog is open.</p>
      </DialogContent>
    </Dialog>
  );
}
