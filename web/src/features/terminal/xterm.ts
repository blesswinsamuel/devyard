import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

const FONT =
  '"JetBrainsMono Nerd Font", "JetBrainsMono NF", "JetBrains Mono Nerd Font", "JetBrains Mono Variable", "JetBrains Mono", "Symbols Nerd Font Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace';

const ANSI_KEYS = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;

/** xterm theme read from the design tokens, so terminals match the log viewer. */
function terminalTheme(): ITheme {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string) => css.getPropertyValue(name).trim();
  const theme: ITheme = {
    background: v("--terminal"),
    foreground: v("--terminal-foreground"),
    cursor: v("--primary"),
    cursorAccent: v("--terminal"),
    selectionBackground: "rgba(129, 140, 248, 0.3)",
  };
  const t = theme as Record<string, string>;
  for (const key of ANSI_KEYS) {
    t[key] = v(`--ansi-${key}`);
    t[`bright${key[0]!.toUpperCase()}${key.slice(1)}`] = v(`--ansi-bright-${key}`);
  }
  return theme;
}

export interface Xterm {
  term: Terminal;
  fit: () => void;
  refreshTheme: () => void;
  dispose: () => void;
}

/** An xterm bound to `container`, auto-fitting on resize. */
export function createXterm(container: HTMLElement, opts: { disableStdin?: boolean } = {}): Xterm {
  const term = new Terminal({
    fontFamily: FONT,
    fontSize: 12.5,
    lineHeight: 1.2,
    scrollback: 10_000,
    cursorBlink: true,
    allowProposedApi: false,
    disableStdin: opts.disableStdin,
    theme: terminalTheme(),
  });
  const fitAddon = new FitAddon();
  term.loadAddon(fitAddon);
  term.loadAddon(new WebLinksAddon());
  term.open(container);

  const fit = () => {
    if (container.offsetWidth > 0 && container.offsetHeight > 0) {
      try {
        fitAddon.fit();
      } catch {
        // Unmeasurable during layout transitions.
      }
    }
  };
  fit();
  const ro = new ResizeObserver(() => fit());
  ro.observe(container);

  return {
    term,
    fit,
    // Tokens change with the theme class; read them after the class flip.
    refreshTheme: () => requestAnimationFrame(() => (term.options.theme = terminalTheme())),
    dispose: () => {
      ro.disconnect();
      term.dispose();
    },
  };
}
