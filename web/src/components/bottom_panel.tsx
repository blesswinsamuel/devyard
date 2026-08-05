import { Show, createSignal, onCleanup } from "solid-js";
import { Bot, GitBranch, Maximize2, Minimize2, SquareTerminal, X } from "lucide-solid";
import {
  selectedProject,
  panelOpen,
  setPanelOpen,
  panelTab,
  openPanelTab,
  panelHeight,
  setPanelHeight,
  panelMaximized,
  togglePanelMaximized,
} from "~/store";
import { ShellWorkspace } from "~/components/shell_workspace";
import { Tooltip, TooltipTrigger, TooltipContent } from "~/components/ui/tooltip";
import { Button } from "~/components/ui/button";

export function BottomPanel() {
  const [isResizing, setIsResizing] = createSignal(false);

  const startResize = (e: MouseEvent) => {
    e.preventDefault();
    setIsResizing(true);
    const startY = e.clientY;
    const startH = panelHeight();

    const onMouseMove = (moveEvent: MouseEvent) => {
      const deltaY = startY - moveEvent.clientY;
      const nextH = Math.max(150, Math.min(window.innerHeight - 100, startH + deltaY));
      setPanelHeight(nextH);
    };

    const onMouseUp = () => {
      setIsResizing(false);
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    };

    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
  };

  return (
    <Show when={selectedProject() && panelOpen()}>
      <div
        class="relative flex shrink-0 flex-col border-t border-border bg-background transition-all"
        style={{
          height: panelMaximized() ? "100%" : `${panelHeight()}px`,
        }}
      >
        {/* Resize Handle */}
        <Show when={!panelMaximized()}>
          <div
            class="group absolute -top-1.5 left-0 right-0 z-10 flex h-3 cursor-row-resize items-center justify-center"
            onMouseDown={startResize}
          >
            <div class="h-1 w-12 rounded-full bg-border group-hover:bg-primary transition-colors" />
          </div>
        </Show>

        {/* Panel Header */}
        <div class="flex h-9 shrink-0 items-center justify-between border-b border-border/60 bg-muted/30 px-3 text-xs">
          {/* Left Tabs */}
          <div class="flex items-center gap-1">
            <button
              class="flex items-center gap-1.5 rounded px-2.5 py-1 font-medium transition-colors cursor-pointer"
              classList={{
                "bg-background text-foreground shadow-sm": panelTab() === "shell",
                "text-muted-foreground hover:text-foreground": panelTab() !== "shell",
              }}
              onClick={() => openPanelTab("shell")}
            >
              <SquareTerminal class="size-3.5" />
              <span>Shell</span>
            </button>

            <Tooltip>
              <TooltipTrigger
                as="button"
                disabled
                class="flex cursor-not-allowed items-center gap-1.5 rounded px-2.5 py-1 text-muted-foreground/50 opacity-60"
              >
                <GitBranch class="size-3.5" />
                <span>Git</span>
                <span class="rounded bg-muted px-1 text-[10px]">Soon</span>
              </TooltipTrigger>
              <TooltipContent>Git graph, diffs & commits coming soon!</TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                as="button"
                disabled
                class="flex cursor-not-allowed items-center gap-1.5 rounded px-2.5 py-1 text-muted-foreground/50 opacity-60"
              >
                <Bot class="size-3.5" />
                <span>Agents</span>
                <span class="rounded bg-muted px-1 text-[10px]">Soon</span>
              </TooltipTrigger>
              <TooltipContent>AI Coding agents coming soon!</TooltipContent>
            </Tooltip>
          </div>

          {/* Right Action Controls */}
          <div class="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              class="size-6 rounded text-muted-foreground hover:text-foreground"
              onClick={togglePanelMaximized}
              title={panelMaximized() ? "Restore Height" : "Maximize Height"}
            >
              <Show when={panelMaximized()} fallback={<Maximize2 class="size-3.5" />}>
                <Minimize2 class="size-3.5" />
              </Show>
            </Button>
            <Button
              variant="ghost"
              size="icon"
              class="size-6 rounded text-muted-foreground hover:text-foreground"
              onClick={() => setPanelOpen(false)}
              title="Close Panel"
            >
              <X class="size-3.5" />
            </Button>
          </div>
        </div>

        {/* Panel Content Workspace */}
        <div class="relative min-h-0 flex-1">
          <Show when={panelTab() === "shell"}>
            <ShellWorkspace />
          </Show>
        </div>
      </div>
    </Show>
  );
}
