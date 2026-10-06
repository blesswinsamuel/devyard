import { createEffect, onCleanup, onMount, Show, type ParentProps } from "solid-js";
import { useLocation, useNavigate } from "@solidjs/router";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { ConfirmHost } from "~/components/confirm-host";
import { startCrashAwareness } from "~/data/crash";
import { entities } from "~/data/entities";
import { connection, isStale } from "~/data/sync";
import { AddProjectDialog } from "~/features/home/add-project-dialog";
import { CommandPalette } from "~/features/palette/palette";
import { ShortcutsDialog } from "~/features/shortcuts/shortcuts";
import { Sidebar } from "~/features/sidebar/sidebar";
import { BranchDialog } from "~/features/git/branch-dialog";
import { RunArgsDialog } from "~/features/task/run-args-dialog";
import { Dock } from "~/features/terminal/dock";
import { isMobile } from "~/lib/media";
import { targetFromPath } from "~/lib/paths";
import { cn } from "~/lib/utils";
import { ConfigErrorBanner, ReconnectBanner } from "./reconnect-banner";
import { bindNavigate, setRouteTarget } from "./runtime";
import { installShortcuts } from "./shortcuts";
import { TopBar } from "./top-bar";
import { mobileNavOpen, setMobileNavOpen, sidebarCollapsed } from "./ui-state";

/** Root layout: sidebar + main + dock, plus app-wide overlays. */
export function Shell(props: ParentProps) {
  const navigate = useNavigate();
  const location = useLocation();
  bindNavigate(navigate);
  createEffect(() => setRouteTarget(targetFromPath(location.pathname)));

  onMount(() => {
    connection.start();
    const offKeys = installShortcuts();
    const offCrash = startCrashAwareness((path) => navigate(path));
    onCleanup(() => {
      offKeys();
      offCrash();
    });
  });

  return (
    <div class="flex h-full">
      <a
        href="#main"
        class="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-primary focus:px-3 focus:py-1.5 focus:text-primary-foreground"
      >
        Skip to content
      </a>
      <Show
        when={!isMobile()}
        fallback={
          <Sheet open={mobileNavOpen()} onOpenChange={setMobileNavOpen}>
            <SheetContent side="left" class="w-72 gap-0 p-0" showCloseButton={false}>
              <SheetTitle class="sr-only">Navigation</SheetTitle>
              <Sidebar onNavigate={() => setMobileNavOpen(false)} />
            </SheetContent>
          </Sheet>
        }
      >
        <Show when={!sidebarCollapsed()}>
          <aside class="w-(--sidebar-w) shrink-0 border-r border-sidebar-border" aria-label="Sidebar">
            <Sidebar collapsible />
          </aside>
        </Show>
      </Show>
      <div class="flex min-w-0 flex-1 flex-col">
        <TopBar />
        <ReconnectBanner />
        <ConfigErrorBanner />
        <main
          id="main"
          tabindex="-1"
          class={cn("min-h-0 flex-1 overflow-y-auto outline-none transition-opacity", isStale() && "opacity-60")}
          aria-busy={!entities.state.loaded || undefined}
        >
          {props.children}
        </main>
        <Dock />
      </div>
      <CommandPalette />
      <ConfirmHost />
      <RunArgsDialog />
      <BranchDialog />
      <AddProjectDialog />
      <ShortcutsDialog />
    </div>
  );
}
