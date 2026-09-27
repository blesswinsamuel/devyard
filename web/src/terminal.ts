import { Terminal, type ITerminalOptions, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import type { ColorMode } from "~/components/color-mode";

// Catppuccin ANSI 16-color palettes (Mocha / Latte) for terminal *content*,
// with backgrounds tuned to match the app's surface tokens so panes blend
// into the chrome.
const themes: Record<ColorMode, ITheme> = {
  dark: {
    background: "#101014",
    foreground: "#d5d6dd",
    cursor: "#818cf8",
    cursorAccent: "#101014",
    selectionBackground: "#3b3b52",
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
    background: "#fbfbfc",
    foreground: "#45475a",
    cursor: "#4f46e5",
    cursorAccent: "#fbfbfc",
    selectionBackground: "#cfd2dc",
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

export function terminalTheme(t: ColorMode): ITheme {
  return themes[t];
}

export interface AppTerminal extends Terminal {
  fit: () => void;
}

export function createTerminal(
  container: HTMLElement,
  initialTheme: ColorMode,
  options?: Partial<ITerminalOptions>
): AppTerminal {
  const term = new Terminal({
    fontFamily:
      '"JetBrainsMono Nerd Font", "JetBrainsMono NF", "JetBrains Mono Nerd Font", "JetBrainsMonoNL Nerd Font", "JetBrainsMonoNL NF", "Hack Nerd Font", "Hack NF", "FiraCode Nerd Font", "FiraCode NF", "CaskaydiaCove Nerd Font", "CaskaydiaCove NF", "CascadiaCode Nerd Font", "MesloLGS NF", "MesloLGM Nerd Font", "JetBrains Mono Variable", "JetBrains Mono", "Symbols Nerd Font Mono", "Symbols Nerd Font", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    fontSize: 12.5,
    lineHeight: 1.25,
    scrollback: 5000,
    cursorBlink: true,
    theme: themes[initialTheme],
    ...options,
  });
  const fitAddon = new FitAddon();
  term.loadAddon(fitAddon);
  term.loadAddon(new WebLinksAddon());
  term.open(container);

  const safeFit = () => {
    if (container.offsetWidth > 0 && container.offsetHeight > 0) {
      try {
        fitAddon.fit();
      } catch {
        // Ignore unmeasured layout exceptions during transitions
      }
    }
  };
  safeFit();

  const resizeObserver = new ResizeObserver(safeFit);
  resizeObserver.observe(container);

  const origDispose = term.dispose.bind(term);
  (term as unknown as { fit: () => void }).fit = safeFit;
  term.dispose = () => {
    resizeObserver.disconnect();
    origDispose();
  };

  return term as AppTerminal;
}
