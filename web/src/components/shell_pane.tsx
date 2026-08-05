import { createEffect, createSignal, onCleanup, onMount } from "solid-js";
import { Columns2, Rows2, X } from "lucide-solid";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { theme } from "~/store";
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

    // Initial PTY spawn
    spawnTerminal(props.id, props.project, t.cols, t.rows);

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    // Auto-fit on window resize or layout changes
    const ro = new ResizeObserver(() => {
      if (props.active && term) {
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
      });
    }
  });

  return (
    <div class="group relative flex h-full w-full flex-col overflow-hidden border border-border/40 bg-background">
      {/* Pane Header */}
      <div class="flex h-7 shrink-0 items-center justify-between border-b border-border/40 bg-muted/30 px-2 text-xs text-muted-foreground">
        <span class="truncate font-mono text-[11px]">
          {props.id} {exited() ? "(exited)" : ""}
        </span>
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
      <div class="relative min-h-0 flex-1 p-1">
        <div ref={container} class="h-full w-full overflow-hidden bg-background" />
      </div>
    </div>
  );
}
