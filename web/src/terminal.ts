import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { theme } from "./store";

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
    magenta: "#cba6f7",
    cyan: "#94e2d5",
    white: "#cdd6f4",
    brightBlack: "#585b70",
    brightRed: "#f38ba8",
    brightGreen: "#a6e3a1",
    brightYellow: "#f9e2af",
    brightBlue: "#89b4fa",
    brightMagenta: "#cba6f7",
    brightCyan: "#94e2d5",
    brightWhite: "#eff1f5",
  },
  light: {
    background: "#eff1f5",
    foreground: "#4c4f69",
    cursor: "#1e66f5",
    cursorAccent: "#eff1f5",
    selectionBackground: "#ccd0da",
    black: "#bcc0cc",
    red: "#d20f39",
    green: "#40a02b",
    yellow: "#df8e1d",
    blue: "#1e66f5",
    magenta: "#8839ef",
    cyan: "#04a5e5",
    white: "#4c4f69",
    brightBlack: "#8c8fa1",
    brightRed: "#d20f39",
    brightGreen: "#40a02b",
    brightYellow: "#df8e1d",
    brightBlue: "#1e66f5",
    brightMagenta: "#8839ef",
    brightCyan: "#04a5e5",
    brightWhite: "#eff1f5",
  },
};

export function terminalTheme(t: "dark" | "light"): ITheme {
  return themes[t];
}

export function createTerminal(container: HTMLElement): Terminal {
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
  fitAddon.fit();

  const resizeObserver = new ResizeObserver(() => fitAddon.fit());
  resizeObserver.observe(container);

  const origDispose = term.dispose.bind(term);
  term.dispose = () => {
    resizeObserver.disconnect();
    origDispose();
  };

  return term;
}
