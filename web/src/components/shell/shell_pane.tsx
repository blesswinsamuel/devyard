import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Columns2, GripVertical, RotateCcw, Rows2, X } from "lucide-solid";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { theme } from "~/stores/app";
import {
  closeTerminal,
  resizeTerminal,
  sendTerminalInput,
  spawnTerminal,
  subscribeTerminal,
} from "~/lib/ws";
import { moveShellPane } from "~/stores/shells";
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
    const t = createTerminal(container, theme());
    term = t;

    const dataSub = t.onData((data) => sendTerminalInput(props.id, data));
    const resizeSub = t.onResize(({ cols, rows }) => resizeTerminal(props.id, cols, rows));
    const unsubWS = subscribeTerminal(
      props.id,
      (output) => {
        t.write(output);
        setExited(false);
      },
      () => {
        setExited(true);
        t.write("\r\n\x1b[33m[Process exited — restart?]\x1b[0m\r\n");
      }
    );

    t.fit();
    spawnTerminal(props.id, props.project, t.cols, t.rows);
    if (props.active) requestAnimationFrame(() => t.focus());

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    onCleanup(() => {
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
    if (x < rect.width * 0.25) setDropZone("left");
    else if (x > rect.width * 0.75) setDropZone("right");
    else if (y < rect.height * 0.25) setDropZone("top");
    else if (y > rect.height * 0.75) setDropZone("bottom");
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
      class="group relative flex h-full w-full flex-col overflow-hidden rounded-lg border bg-card"
      onClick={() => term?.focus()}
      onDragOver={handleDragOver}
      onDragLeave={() => setDropZone(null)}
      onDrop={handleDrop}
    >
      <Show when={dropZone()}>
        <div
          class="pointer-events-none absolute inset-0 z-30 border-2 border-primary bg-primary/20 transition-all"
          classList={{
            "right-1/2": dropZone() === "left",
            "left-1/2": dropZone() === "right",
            "bottom-1/2": dropZone() === "top",
            "top-1/2": dropZone() === "bottom",
          }}
        />
      </Show>

      {/* Pane header */}
      <div class="flex h-7 shrink-0 items-center justify-between border-b bg-muted/40 px-1.5 text-[11px] text-muted-foreground">
        <div
          class="flex select-none items-center gap-1"
          draggable="true"
          onDragStart={(e) => e.dataTransfer?.setData("application/x-local-compose-pane", props.id)}
          title="Drag to move or split pane"
        >
          <GripVertical class="size-3 cursor-grab active:cursor-grabbing" />
          <span class="font-mono">shell</span>
          <Show when={exited()}>
            <Button
              variant="outline"
              size="xs"
              class="ml-1 h-4 gap-0.5 px-1 font-medium text-[10px]"
              onClick={handleRestart}
            >
              <RotateCcw class="size-2.5" />
              restart
            </Button>
          </Show>
        </div>

        <div class="flex items-center gap-0.5 opacity-50 transition-opacity group-hover:opacity-100">
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" onClick={props.onSplitRight} title="Split right">
            <Columns2 class="!size-3" />
          </Button>
          <Button variant="ghost" size="icon-sm" class="text-muted-foreground" onClick={props.onSplitDown} title="Split down">
            <Rows2 class="!size-3" />
          </Button>
          <Button variant="ghost" size="icon-sm" class="hover:text-destructive" onClick={props.onClose} title="Close pane">
            <X class="!size-3" />
          </Button>
        </div>
      </div>

      {/* Terminal */}
      <div class="relative min-h-0 flex-1 p-0.5" onClick={() => term?.focus()}>
        <div ref={container} class="h-full w-full overflow-hidden rounded" />
      </div>
    </div>
  );
}
