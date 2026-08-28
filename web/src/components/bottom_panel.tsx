import { Show, createSignal } from "solid-js";
import { Maximize2, Minimize2, SquareTerminal, X } from "lucide-solid";
import { selectedProject } from "~/stores/nav";
import { panelHeight, panelMaximized, panelOpen, setPanelHeight, setPanelOpen, togglePanelMaximized } from "~/stores/app";
import { ShellTabBar, ShellWorkspace } from "~/components/shell/shell_workspace";
import { Button } from "~/components/ui/button";

export function BottomPanel() {
  const [resizing, setResizing] = createSignal(false);

  const startResize = (e: PointerEvent) => {
    e.preventDefault();
    setResizing(true);
    const startY = e.clientY;
    const startH = panelHeight();

    const onPointerMove = (moveEvent: PointerEvent) => {
      const nextH = Math.max(150, Math.min(window.innerHeight - 100, startH + (startY - moveEvent.clientY)));
      setPanelHeight(nextH);
    };
    const onPointerUp = () => {
      setResizing(false);
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
    };
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    window.addEventListener("pointercancel", onPointerUp);
  };

  return (
    <Show when={selectedProject() && panelOpen()}>
      <div
        class="relative flex shrink-0 flex-col border-t bg-card"
        style={{ height: panelMaximized() ? "100%" : `${panelHeight()}px` }}
      >
        <Show when={!panelMaximized()}>
          <div
            class="group absolute -top-1 left-0 right-0 z-10 flex h-2 cursor-row-resize touch-none items-center justify-center"
            onPointerDown={startResize}
          >
            <div
              class="h-1 w-10 rounded-full bg-border-strong transition-colors group-hover:bg-primary"
              classList={{ "bg-primary/60": resizing() }}
            />
          </div>
        </Show>

        {/* Panel header */}
        <div class="flex h-8 shrink-0 items-center justify-between gap-2 border-b bg-muted/30 px-1.5">
          <div class="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto px-0.5">
            <SquareTerminal class="ml-0.5 size-3.5 shrink-0 text-muted-foreground" />
            <ShellTabBar />
          </div>
          <div class="flex shrink-0 items-center gap-0.5 pr-0.5">
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" onClick={togglePanelMaximized} title={panelMaximized() ? "Restore" : "Maximize"}>
              <Show when={panelMaximized()} fallback={<Maximize2 class="!size-3" />}>
                <Minimize2 class="!size-3" />
              </Show>
            </Button>
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" onClick={() => setPanelOpen(false)} title="Close panel (t)">
              <X class="!size-3" />
            </Button>
          </div>
        </div>

        <div class="relative min-h-0 flex-1">
          <ShellWorkspace />
        </div>
      </div>
    </Show>
  );
}
