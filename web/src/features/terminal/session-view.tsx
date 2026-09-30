import { createEffect, createSignal, on, onCleanup, onMount, Show, type JSX } from "solid-js";
import { LoaderCircle, Unplug } from "lucide-solid";
import { AttachSession, type SessionExit, type SessionPhase, type SessionTarget } from "~/data/sessions";
import { resolvedTheme } from "~/lib/theme";
import { cn } from "~/lib/utils";
import { LineInput } from "./stdin";
import { createXterm, type Xterm } from "./xterm";

export interface SessionViewProps {
  target: SessionTarget;
  class?: string;
  /** Focus the terminal once ready. */
  autoFocus?: boolean;
  /** For hidden dock panes: refit and focus when this becomes true. */
  active?: boolean;
  onSessionId?: (id: string) => void;
  onExit?: (exit: SessionExit) => void;
  /** Buttons shown on the exited/ended overlay (e.g. "New terminal", "Run again"). */
  endActions?: (phase: "exited" | "ended") => JSX.Element;
}

/**
 * Live xterm bound to an attach session. The target is fixed for the
 * component's lifetime; parents re-key it to switch targets.
 */
export function SessionView(props: SessionViewProps) {
  let container!: HTMLDivElement;
  let xterm: Xterm | undefined;
  let session: AttachSession | undefined;
  const [phase, setPhase] = createSignal<SessionPhase>("connecting");
  const [detail, setDetail] = createSignal("");
  const [tty, setTty] = createSignal(true);
  const [stdin, setStdin] = createSignal(true);
  const [stdinClosed, setStdinClosed] = createSignal(false);
  const [exit, setExit] = createSignal<SessionExit | null>(null);

  onMount(() => {
    xterm = createXterm(container);
    const term = xterm.term;
    session = new AttachSession(
      props.target,
      { cols: term.cols, rows: term.rows },
      {
        onOutput: (data) => term.write(data),
        onReady: (info) => {
          // The server replays scrollback on every attach.
          term.reset();
          setTty(info.tty);
          setStdin(info.stdin);
          setStdinClosed(false);
          term.options.disableStdin = !info.tty || !info.stdin;
          term.options.convertEol = !info.tty;
          if (info.sessionId) props.onSessionId?.(info.sessionId);
          if (props.autoFocus !== false && info.tty && info.stdin) term.focus();
        },
        onExit: (e) => {
          setExit(e);
          props.onExit?.(e);
        },
        onPhase: (p, d) => {
          setPhase(p);
          setDetail(d);
        },
      },
    );
    term.onData((d) => session?.input(d));
    term.onBinary((d) => session?.input(Uint8Array.from(d, (c) => c.charCodeAt(0))));
    term.onResize(({ cols, rows }) => session?.resize(cols, rows));
  });

  createEffect(on(resolvedTheme, () => xterm?.refreshTheme(), { defer: true }));
  createEffect(
    on(
      () => props.active,
      (active) => {
        if (!active || !xterm) return;
        requestAnimationFrame(() => {
          xterm?.fit();
          if (tty() && phase() === "ready") xterm?.term.focus();
        });
      },
      { defer: true },
    ),
  );

  onCleanup(() => {
    session?.detach();
    xterm?.dispose();
  });

  const ended = () => phase() === "exited" || phase() === "ended";

  return (
    <div class={cn("relative flex min-h-0 flex-col overflow-hidden bg-terminal", props.class)}>
      <Show when={phase() === "reconnecting"}>
        <div class="flex items-center gap-2 border-b bg-warning/10 px-3 py-1 text-2xs text-warning" role="status">
          <LoaderCircle class="size-3 animate-spin" /> Connection lost — reattaching…
        </div>
      </Show>
      <div ref={container} class="min-h-0 flex-1 px-2 pt-1" />
      <Show when={phase() === "ready" && !tty() && stdin()}>
        <LineInput
          disabled={stdinClosed()}
          placeholder={stdinClosed() ? "stdin closed" : undefined}
          onSend={(line) => session?.input(line)}
          onEof={() => {
            session?.eof();
            setStdinClosed(true);
          }}
        />
      </Show>
      <Show when={phase() === "connecting"}>
        <div class="absolute inset-0 flex items-center justify-center gap-2 text-ui text-muted-foreground">
          <LoaderCircle class="size-4 animate-spin" /> Connecting…
        </div>
      </Show>
      <Show when={ended()}>
        <div class="absolute inset-x-0 bottom-0 flex flex-wrap items-center gap-3 border-t bg-card/95 px-3 py-2 text-ui backdrop-blur-sm" role="status">
          <Unplug class="size-4 text-muted-foreground" />
          <span class="min-w-0 flex-1">
            <Show
              when={phase() === "exited"}
              fallback={
                <>
                  <span class="font-medium">Session ended.</span>{" "}
                  <span class="text-muted-foreground">{detail()}</span>
                </>
              }
            >
              <span class={cn("font-medium", exit()?.exitCode ? "text-destructive" : undefined)}>
                Process exited{exit() ? ` with code ${exit()!.exitCode}` : ""}.
              </span>{" "}
              <span class="text-muted-foreground">{exit()?.message || detail()}</span>
            </Show>
          </span>
          {props.endActions?.(phase() as "exited" | "ended")}
        </div>
      </Show>
    </div>
  );
}
