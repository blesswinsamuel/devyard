import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Columns2, GripVertical, RotateCcw, Rows2, X } from "lucide-solid";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { theme, moveShellPane } from "~/store";
import {
  subscribeTerminal,
  spawnTerminal,
  sendTerminalInput,
  resizeTerminal,
  closeTerminal,
} from "~/ws";
import { Button } from "~/components/ui/button";

export function ShellPane(props: {
  id: string;
  project: string;
  active: boolean;
  onSplitRight: () => void;
  onSplitDown: () => void;
  onClose: () => void;
}) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;
  const [exited, setExited] = createSignal(false);
  const [dropZone, setDropZone] = createSignal<"left" | "right" | "top" | "bottom" | "swap" | null>(null);

  const handleRestart = () => {
    if (!term) return;
    setExited(false);
    term.clear();
    term.write("\x1b[2J\x1b[3J\x1b[H");
    spawnTerminal(props.id, props.project, term.cols, term.rows);
    term.focus();
  };

  onMount(() => {
    const t = createTerminal(container);
    term = t;

    // Direct user typing -> PTY stdin
    const dataSub = t.onData((data) => {
      sendTerminalInput(props.id, data);
    });

    // Terminal dimension changes -> PTY resize
    const resizeSub = t.onResize(({ cols, rows }) => {
      resizeTerminal(props.id, cols, rows);
    });

    // PTY output/exit events from server
    const unsubWS = subscribeTerminal(
      props.id,
      (output) => {
        t.write(output);
      },
      () => {
        setExited(true);
        t.write("\r\n\x1b[33m[Process exited]\x1b[0m\r\n");
      }
    );

    // Initial fit before spawn to send correct dimensions
    t.fit();

    // Initial PTY spawn
    spawnTerminal(props.id, props.project, t.cols, t.rows);

    if (props.active) {
      requestAnimationFrame(() => {
        t.focus();
      });
    }

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    // Auto-fit on window resize or layout changes
    const ro = new ResizeObserver(() => {
      if (term) {
        requestAnimationFrame(() => {
          term?.fit();
        });
      }
    });
    ro.observe(container);

    onCleanup(() => {
      ro.disconnect();
      dataSub.dispose();
      resizeSub.dispose();
      unsubWS();
      t.dispose();
      term = null;
    });
  });

  createEffect(() => {
    if (props.active && term) {
      requestAnimationFrame(() => {
        term?.fit();
        term?.focus();
      });
    }
  });

  const handleDragOver = (e: DragEvent) => {
    if (!e.dataTransfer?.types.includes("application/x-local-compose-pane")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const w = rect.width;
    const h = rect.height;

    if (x < w * 0.25) setDropZone("left");
    else if (x > w * 0.75) setDropZone("right");
    else if (y < h * 0.25) setDropZone("top");
    else if (y > h * 0.75) setDropZone("bottom");
    else setDropZone("swap");
  };

  const handleDrop = (e: DragEvent) => {
    const sourceId = e.dataTransfer?.getData("application/x-local-compose-pane");
    const zone = dropZone();
    setDropZone(null);
    if (!sourceId || !zone || sourceId === props.id) return;
    e.preventDefault();
    e.stopPropagation();
    moveShellPane(props.project, sourceId, props.id, zone);
  };

  return (
    <div
      class="group relative flex h-full w-full flex-col overflow-hidden border border-border/40 bg-background"
      onClick={() => term?.focus()}
      onDragOver={handleDragOver}
      onDragLeave={() => setDropZone(null)}
      onDrop={handleDrop}
    >
      {/* Visual Drop Zone Highlight */}
      <Show when={dropZone()}>
        <div
          class="pointer-events-none absolute inset-0 z-30 bg-primary/20 border-2 border-primary transition-all"
          classList={{
            "right-1/2": dropZone() === "left",
            "left-1/2": dropZone() === "right",
            "bottom-1/2": dropZone() === "top",
            "top-1/2": dropZone() === "bottom",
          }}
        />
      </Show>

      {/* Pane Header */}
      <div class="flex h-7 shrink-0 items-center justify-between border-b border-border/40 bg-muted/30 px-2 text-xs text-muted-foreground">
        <div
          class="flex items-center gap-1.5 cursor-grab active:cursor-grabbing select-none"
          draggable="true"
          onDragStart={(e) => {
            e.dataTransfer?.setData("application/x-local-compose-pane", props.id);
          }}
          title="Drag to move or split pane"
        >
          <GripVertical class="size-3.5 text-muted-foreground/60 hover:text-foreground" />
          <span class="truncate font-mono text-[11px]">
            {props.id} {exited() ? "(exited)" : ""}
          </span>
          <Show when={exited()}>
            <Button
              variant="outline"
              size="icon"
              class="h-5 px-1 text-[11px] gap-1 hover:bg-muted"
              onClick={handleRestart}
              title="Restart Shell Process"
            >
              <RotateCcw class="size-3" />
              <span>Restart</span>
            </Button>
          </Show>
        </div>

        <div class="flex items-center gap-0.5 opacity-60 group-hover:opacity-100 transition-opacity">
          <Button
            variant="ghost"
            size="icon"
            class="size-5 rounded text-muted-foreground hover:text-foreground"
            onClick={props.onSplitRight}
            title="Split Right (Vertical)"
          >
            <Columns2 class="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            class="size-5 rounded text-muted-foreground hover:text-foreground"
            onClick={props.onSplitDown}
            title="Split Down (Horizontal)"
          >
            <Rows2 class="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            class="size-5 rounded text-muted-foreground hover:text-destructive"
            onClick={props.onClose}
            title="Close Pane"
          >
            <X class="size-3.5" />
          </Button>
        </div>
      </div>

      {/* XTerm Container */}
      <div class="relative min-h-0 flex-1 p-1" onClick={() => term?.focus()}>
        <div ref={container} class="h-full w-full overflow-hidden bg-background" />
      </div>
    </div>
  );
}
