import { Show, createEffect, createSignal, onCleanup, onMount } from "solid-js";
import { RotateCcw } from "lucide-solid";
import { type AppTerminal, createTerminal, terminalTheme } from "~/terminal";
import { theme } from "~/stores/app";
import {
  closeTerminal,
  resizeTerminal,
  sendTerminalInput,
  spawnTerminal,
  subscribeTerminal,
} from "~/lib/ws";
import { Button } from "~/components/ui/button";

/**
 * One terminal session rendered into an xterm instance — the content of a
 * terminal tab. Owns the pty identified by `termId`; spawning is idempotent
 * per session id, so restored tabs re-attach or respawn automatically.
 */
export function TerminalContent(props: { project: string; termId: string; active: boolean }) {
  let container!: HTMLDivElement;
  let term: AppTerminal | null = null;
  const [exited, setExited] = createSignal(false);
  // Tab rows remount when their tab object is replaced; pending pty output
  // must not be written after dispose.
  let disposed = false;

  const handleRestart = () => {
    if (!term || disposed) return;
    setExited(false);
    term.clear();
    term.write("\x1b[2J\x1b[3J\x1b[H");
    spawnTerminal(props.termId, props.project, term.cols, term.rows);
    term.focus();
  };

  onMount(() => {
    const t = createTerminal(container, theme());
    term = t;

    const dataSub = t.onData((data) => sendTerminalInput(props.termId, data));
    const resizeSub = t.onResize(({ cols, rows }) => resizeTerminal(props.termId, cols, rows));
    const unsubWS = subscribeTerminal(
      props.termId,
      (output) => {
        if (disposed) return;
        t.write(output);
        setExited(false);
      },
      () => {
        if (disposed) return;
        setExited(true);
        t.write("\r\n\x1b[33m[Process exited — restart?]\x1b[0m\r\n");
      }
    );

    t.fit();
    spawnTerminal(props.termId, props.project, t.cols, t.rows);
    if (props.active) requestAnimationFrame(() => t.focus());

    createEffect(() => {
      t.options.theme = terminalTheme(theme());
    });

    onCleanup(() => {
      disposed = true;
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

  onCleanup(() => {
    if (term) closeTerminal(props.termId);
  });

  return (
    <div class="relative h-full w-full overflow-hidden bg-card" classList={{ hidden: !props.active }}>
      <div ref={container} class="h-full w-full p-0.5" />
      <Show when={exited()}>
        <Button
          variant="outline"
          size="xs"
          class="absolute right-3 top-2 gap-0.5 font-medium text-[10px]"
          onClick={handleRestart}
        >
          <RotateCcw class="size-2.5" />
          restart
        </Button>
      </Show>
    </div>
  );
}
