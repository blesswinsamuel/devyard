import { onCleanup, Show } from "solid-js";
import { start } from "~/stores/nav";
import { setupHotkeys } from "~/hotkeys";
import { Sidebar } from "~/components/sidebar";
import { Main } from "~/components/main";
import { HelpOverlay } from "~/components/help";
import { AddProjectModal } from "~/components/add_project_modal";
import { DaemonStatusModal } from "~/components/daemon_modal";
import { Toasts } from "~/components/toasts";
import { ReconnectBanner } from "~/components/reconnect_banner";

export function App() {
  const stop = start();
  const stopHotkeys = setupHotkeys();
  onCleanup(() => {
    stop();
    stopHotkeys();
  });

  return (
    <div class="flex h-full w-full overflow-hidden bg-background text-foreground">
      <Sidebar />
      <Main />
      <HelpOverlay />
      <AddProjectModal />
      <DaemonStatusModal />
      <ReconnectBanner />
      <Toasts />
      <Show when={false}>{null}</Show>
    </div>
  );
}
