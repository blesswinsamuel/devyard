import { For, Show } from "solid-js";
import { A, useLocation } from "@solidjs/router";
import { Menu, Monitor, Moon, PanelBottom, PanelLeft, Search, Sun } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Shortcut } from "~/components/shortcut";
import { dock, dockActions } from "~/data/dock";
import { isMobile } from "~/lib/media";
import { paths } from "~/lib/paths";
import { cycleTheme, themePref } from "~/lib/theme";
import { cn } from "~/lib/utils";
import { routeTarget } from "./runtime";
import { setMobileNavOpen, setPaletteOpen, setSidebarCollapsed, sidebarCollapsed } from "./ui-state";

function Crumbs() {
  const loc = useLocation();
  const crumbs = () => {
    const t = routeTarget();
    const list: { label: string; href?: string }[] = [{ label: "Projects", href: paths.home() }];
    if (loc.pathname === paths.settings()) list.push({ label: "Settings" });
    if (t.kind === "app") return list;
    list.push({ label: t.project, href: paths.project(t.project) });
    if (t.kind === "service") list.push({ label: t.name });
    if (t.kind === "task") list.push({ label: `task: ${t.name}` });
    const git = t.kind === "project" ? loc.pathname.match(/\/git(?:\/commits\/([^/]+))?\/?$/) : null;
    if (git) {
      list.push({ label: "Git", href: paths.git(t.project) });
      const hash = git[1] ? decodeURIComponent(git[1]) : "";
      if (hash) list.push({ label: hash === "WORKDIR" ? "Uncommitted changes" : hash.slice(0, 7) });
    }
    return list;
  };
  return (
    <nav aria-label="Breadcrumb" class="min-w-0">
      <ol class="flex min-w-0 items-center gap-1.5 text-ui">
        <For each={crumbs()}>
          {(c, i) => (
            <li class="flex min-w-0 items-center gap-1.5">
              <Show when={i() > 0}>
                <span class="text-muted-foreground/60" aria-hidden="true">
                  /
                </span>
              </Show>
              <Show
                when={c.href && i() < crumbs().length - 1}
                fallback={
                  <span class="truncate font-medium" aria-current="page">
                    {c.label}
                  </span>
                }
              >
                <A href={c.href!} class="focus-ring truncate rounded-sm text-muted-foreground hover:text-foreground">
                  {c.label}
                </A>
              </Show>
            </li>
          )}
        </For>
      </ol>
    </nav>
  );
}

export function TopBar() {
  const ThemeIcon = () => {
    const pref = themePref();
    return pref === "light" ? <Sun /> : pref === "dark" ? <Moon /> : <Monitor />;
  };
  return (
    <header class="flex h-(--header-h) shrink-0 items-center gap-2 border-b bg-background px-3">
      <Show when={isMobile()}>
        <Button variant="ghost" size="icon-sm" aria-label="Open navigation" onClick={() => setMobileNavOpen(true)}>
          <Menu />
        </Button>
      </Show>
      <Show when={!isMobile() && sidebarCollapsed()}>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Show sidebar"
          title="Show sidebar (⌘B)"
          onClick={() => setSidebarCollapsed(false)}
        >
          <PanelLeft />
        </Button>
      </Show>
      <Crumbs />
      <div class="ml-auto flex items-center gap-1">
        <Button
          variant="outline"
          size="sm"
          class="hidden w-56 justify-start text-muted-foreground md:inline-flex"
          onClick={() => setPaletteOpen(true)}
        >
          <Search />
          Search or run…
          <Shortcut keys="mod+k" class="ml-auto" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          class="md:hidden"
          aria-label="Command palette"
          onClick={() => setPaletteOpen(true)}
        >
          <Search />
        </Button>
        <Show when={dock.tabs.length > 0}>
          <Button
            variant={dock.open ? "secondary" : "ghost"}
            size="sm"
            aria-label={dock.open ? "Hide dock" : "Show dock"}
            aria-pressed={dock.open}
            title="Toggle dock (⌘J)"
            onClick={() => dockActions.toggle()}
          >
            <PanelBottom />
            <span class={cn("tabular text-2xs", !dock.open && "text-muted-foreground")}>{dock.tabs.length}</span>
          </Button>
        </Show>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Theme: ${themePref()} (click to change)`}
          title={`Theme: ${themePref()}`}
          onClick={cycleTheme}
        >
          <ThemeIcon />
        </Button>
      </div>
    </header>
  );
}
