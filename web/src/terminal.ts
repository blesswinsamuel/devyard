import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { theme } from "./store";

// Official Catppuccin ANSI 16-color palettes (Mocha / Latte). Using the
// palette's ANSI mapping — not surface/text roles — so \e[30m–\e[37m and
// bright variants match a real Catppuccin terminal.
const themes: Record<"dark" | "light", ITheme> = {
  dark: {
    background: "#1e1e2e",
    foreground: "#cdd6f4",
    cursor: "#89b4fa",
    cursorAccent: "#1e1e2e",
    selectionBackground: "#45475a",
    black: "#45475a",
    red: "#f38ba8",
    green: "#a6e3a1",
    yellow: "#f9e2af",
    blue: "#89b4fa",
    magenta: "#f5c2e7",
    cyan: "#94e2d5",
    white: "#bac2de",
    brightBlack: "#585b70",
    brightRed: "#f38ba8",
    brightGreen: "#a6e3a1",
    brightYellow: "#f9e2af",
    brightBlue: "#89b4fa",
    brightMagenta: "#f5c2e7",
    brightCyan: "#94e2d5",
    brightWhite: "#a6adc8",
  },
  light: {
    background: "#eff1f5",
    foreground: "#4c4f69",
    cursor: "#1e66f5",
    cursorAccent: "#eff1f5",
    selectionBackground: "#ccd0da",
    black: "#5c5f77",
    red: "#d20f39",
    green: "#40a02b",
    yellow: "#df8e1d",
    blue: "#1e66f5",
    magenta: "#ea76cb",
    cyan: "#179299",
    white: "#acb0be",
    brightBlack: "#6c6f85",
    brightRed: "#d20f39",
    brightGreen: "#40a02b",
    brightYellow: "#df8e1d",
    brightBlue: "#1e66f5",
    brightMagenta: "#ea76cb",
    brightCyan: "#179299",
    brightWhite: "#bcc0cc",
  },
};

export function terminalTheme(t: "dark" | "light"): ITheme {
  return themes[t];
}

export interface AppTerminal extends Terminal {
  fit: () => void;
}

export function createTerminal(container: HTMLElement): AppTerminal {
  const term = new Terminal({
    fontFamily: "ui-monospace, SF Mono, SFMono-Regular, Menlo, monospace",
    fontSize: 13,
    scrollback: 5000,
    convertEol: true,
    theme: themes[theme()],
  });
  const fitAddon = new FitAddon();
  term.loadAddon(fitAddon);
  term.loadAddon(new WebLinksAddon());
  term.open(container);
  if (container.offsetWidth > 0 && container.offsetHeight > 0) {
    fitAddon.fit();
  }

  const safeFit = () => {
    if (container.offsetWidth > 0 && container.offsetHeight > 0) {
      fitAddon.fit();
    }
  };

  const resizeObserver = new ResizeObserver(safeFit);
  resizeObserver.observe(container);

  const origDispose = term.dispose.bind(term);
  (term as any).fit = safeFit;
  term.dispose = () => {
    resizeObserver.disconnect();
    origDispose();
  };

  return term as AppTerminal;
}
