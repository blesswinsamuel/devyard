import { For, Show, createMemo } from "solid-js";
import { ChevronRight, Boxes, MoreVertical, Power, RotateCcw, Skull, Sun, Moon } from "lucide-solid";
import {
  projects,
  services,
  expanded,
  selectedProject,
  selectedService,
  toggleProject,
  selectProject,
  selectService,
  stopProject,
  restartService,
  stopService,
  killService,
  theme,
  setTheme,
  wsStatus,
} from "~/store";
import { statusDot, statusTone, serviceMeta } from "~/lib/status";
import { cn } from "~/lib/utils";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Switch } from "~/components/ui/switch";
import {
  CollapsibleRoot,
  CollapsibleTrigger,
  CollapsibleContent,
} from "~/components/ui/collapsible";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "~/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "~/components/ui/dropdown-menu";

function WsDot() {
  const color = () =>
    wsStatus() === "open"
      ? "bg-success"
      : wsStatus() === "connecting"
        ? "bg-warning animate-pulse"
        : "bg-destructive";
  return <span class={cn("inline-block size-2 shrink-0", color())} />;
}

function ServiceRow(props: { project: string; name: string }) {
  const service = createMemo(() =>
    (services()[props.project] ?? []).find((s) => s.name === props.name)
  );
  const selected = () =>
    selectedService() === props.name && selectedProject() === props.project;

  return (
    <Show when={service()}>
      {(s) => (
        <div class="group relative flex items-stretch">
          <button
            type="button"
            onClick={() => selectService(props.project, props.name)}
            class={cn(
              "flex min-w-0 flex-1 flex-col gap-0.5 px-3 py-1.5 pl-7 text-left transition-colors",
              "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
              selected() && "bg-muted text-foreground"
            )}
          >
            <span class="flex min-w-0 items-center gap-2 text-sm">
              <span class={cn("inline-block size-1.5 shrink-0", statusDot(s().status))} />
              <span class="truncate font-medium">{props.name}</span>
              <Show when={s().status === "exited" && s().exit_code !== 0}>
                <span class="text-destructive">({s().exit_code})</span>
              </Show>
            </span>
            <span class="truncate pl-3.5 text-xs text-muted-foreground">
              {serviceMeta(s())}
            </span>
          </button>

          <div
            class={cn(
              "absolute right-1 top-1/2 flex -translate-y-1/2 items-center transition-opacity",
              selected() ? "opacity-100" : "opacity-0 group-hover:opacity-100"
            )}
          >
            <DropdownMenu>
              <DropdownMenuTrigger
                as={Button}
                variant="ghost"
                size="icon-sm"
                class="border border-transparent hover:border-border"
              >
                <MoreVertical />
                <span class="sr-only">Actions</span>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem onSelect={() => restartService(props.project, props.name)}>
                  <RotateCcw />
                  Restart
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => stopService(props.project, props.name)}>
                  <Power />
                  Stop
                </DropdownMenuItem>
                <DropdownMenuItem
                  class="text-destructive focus:text-destructive"
                  onSelect={() => killService(props.project, props.name)}
                >
                  <Skull />
                  Kill
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      )}
    </Show>
  );
}

function ProjectItem(props: { name: string }) {
  const project = createMemo(() => projects().find((p) => p.name === props.name));
  const serviceNames = createMemo(() =>
    (services()[props.name] ?? []).map((s) => s.name)
  );
  const isOpen = () => expanded().has(props.name);
  const selected = () => selectedProject() === props.name;

  return (
    <Show when={project()}>
      {(p) => (
        <CollapsibleRoot
          open={isOpen()}
          onOpenChange={() => toggleProject(props.name)}
        >
          <div
            class={cn(
              "group flex items-center border-l-2 transition-colors",
              selected() ? "border-primary bg-muted" : "border-transparent"
            )}
          >
            <CollapsibleTrigger class="group flex h-7 w-7 shrink-0 items-center justify-center text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring data-[expanded]:text-foreground">
              <ChevronRight class="transition-transform group-data-[expanded]:rotate-90" />
              <span class="sr-only">Toggle</span>
            </CollapsibleTrigger>
            <button
              type="button"
              onClick={() => selectProject(props.name)}
              class="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left text-sm font-medium hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <Boxes class="size-3.5 shrink-0 text-muted-foreground" />
              <span class="truncate">{props.name}</span>
            </button>
            <div class="flex shrink-0 items-center gap-1 pr-1.5">
              <Badge variant={statusTone(p().status)} class="capitalize">
                {p().status}
              </Badge>
              <Tooltip>
                <TooltipTrigger
                  as={Button}
                  variant="ghost"
                  size="icon-sm"
                  class="opacity-0 transition-opacity group-hover:opacity-100"
                  onClick={() => stopProject(props.name)}
                >
                  <Power />
                  <span class="sr-only">Stop project</span>
                </TooltipTrigger>
                <TooltipContent>Stop project</TooltipContent>
              </Tooltip>
            </div>
          </div>
          <CollapsibleContent>
            <div class="border-l border-border py-1">
              <Show
                when={serviceNames().length > 0}
                fallback={
                  <p class="px-7 py-1 text-xs text-muted-foreground">no services</p>
                }
              >
                <For each={serviceNames()}>
                  {(name) => <ServiceRow project={props.name} name={name} />}
                </For>
              </Show>
            </div>
          </CollapsibleContent>
        </CollapsibleRoot>
      )}
    </Show>
  );
}

function ThemeToggle() {
  return (
    <div class="flex items-center gap-2">
      <Show when={theme() === "dark"} fallback={<Sun class="size-4 text-muted-foreground" />}>
        <Moon class="size-4 text-muted-foreground" />
      </Show>
      <Switch checked={theme() === "dark"} onChange={(v) => setTheme(v ? "dark" : "light")} />
    </div>
  );
}

export function Sidebar() {
  const projectNames = createMemo(() => projects().map((p) => p.name));

  return (
    <aside class="flex h-full w-72 shrink-0 flex-col border-r border-border bg-card">
      <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
        <Boxes class="size-5 text-primary" />
        <span class="font-semibold tracking-wide">local-compose</span>
        <span class="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
          <WsDot />
          {wsStatus()}
        </span>
      </header>

      <nav class="flex-1 overflow-y-auto py-2">
        <p class="px-4 pb-1 text-xs uppercase tracking-wider text-muted-foreground">
          Projects
        </p>
        <For each={projectNames()}>
          {(name) => <ProjectItem name={name} />}
        </For>
        <Show when={projectNames().length === 0}>
          <p class="px-4 py-4 text-xs leading-relaxed text-muted-foreground">
            No projects running. Start one with <code class="text-foreground">local-compose up</code>.
          </p>
        </Show>
      </nav>

      <footer class="flex h-11 shrink-0 items-center justify-between border-t border-border px-3">
        <span class="text-xs text-muted-foreground">{theme() === "dark" ? "Dark" : "Light"}</span>
        <ThemeToggle />
      </footer>
    </aside>
  );
}
