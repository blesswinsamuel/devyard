import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

export function createTerminal(container: HTMLElement): Terminal {
  const term = new Terminal({
    fontFamily: "monospace",
    fontSize: 13,
    scrollback: 5000,
    convertEol: true,
  });
  const fitAddon = new FitAddon();
  term.loadAddon(fitAddon);
  term.loadAddon(new WebLinksAddon());
  term.open(container);
  fitAddon.fit();

  const resizeObserver = new ResizeObserver(() => fitAddon.fit());
  resizeObserver.observe(container);

  return term;
}
