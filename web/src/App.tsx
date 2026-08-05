import { For, Show, onCleanup } from "solid-js";
import { start, toasts } from "./store";
import { setupHotkeys } from "./hotkeys";
import { Sidebar } from "./components/sidebar";
import { Main } from "./components/main";
import { HelpOverlay } from "./components/help";

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

      {/* Toasts */}
      <div class="pointer-events-none fixed right-4 top-4 z-[100] flex w-80 flex-col gap-2">
        <For each={toasts()}>
          {(t) => (
            <div
              class="pointer-events-auto border bg-popover px-3 py-2 text-sm shadow-md"
              classList={{
                "border-destructive/60 text-destructive": t.kind === "error",
                "border-border text-foreground": t.kind === "info",
              }}
            >
              {t.message}
            </div>
          )}
        </For>
      </div>
    </div>
  );
}
