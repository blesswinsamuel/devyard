import type { JSX } from "solid-js";

/**
 * ANSI SGR → styled segments for the DOM log viewer.
 *
 * The daemon already strips cursor/erase sequences, but the parser still
 * drops any non-SGR escape (CSI, OSC, two-byte ESC) and C0 controls other
 * than TAB so a stray sequence can never corrupt a row.
 */

export interface AnsiStyle {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  inverse?: boolean;
  strike?: boolean;
}

export interface AnsiSegment {
  text: string;
  /** Undefined when the text uses the default style. */
  style?: AnsiStyle;
}

const BASIC = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;

function basicColor(index: number, bright: boolean): string {
  return `var(--ansi-${bright ? "bright-" : ""}${BASIC[index]})`;
}

const CUBE = [0, 95, 135, 175, 215, 255];

/** xterm 256-colour palette entry as a CSS colour. */
export function color256(n: number): string | undefined {
  if (!Number.isInteger(n) || n < 0 || n > 255) return undefined;
  if (n < 8) return basicColor(n, false);
  if (n < 16) return basicColor(n - 8, true);
  if (n < 232) {
    const i = n - 16;
    return `rgb(${CUBE[Math.floor(i / 36)]}, ${CUBE[Math.floor(i / 6) % 6]}, ${CUBE[i % 6]})`;
  }
  const v = 8 + (n - 232) * 10;
  return `rgb(${v}, ${v}, ${v})`;
}

function isEmptyStyle(s: AnsiStyle): boolean {
  for (const k in s) if (s[k as keyof AnsiStyle] !== undefined) return false;
  return true;
}

function sameStyle(a: AnsiStyle | undefined, b: AnsiStyle | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return (
    a.fg === b.fg &&
    a.bg === b.bg &&
    a.bold === b.bold &&
    a.dim === b.dim &&
    a.italic === b.italic &&
    a.underline === b.underline &&
    a.inverse === b.inverse &&
    a.strike === b.strike
  );
}

/** Parses an extended colour (38/48) starting at params[i]; returns [colour, consumed]. */
function extendedColor(params: string[], i: number): [string | undefined, number] {
  const head = params[i]!;
  // Colon form: "38:5:n" or "38:2::r:g:b" / "38:2:r:g:b" in a single param.
  if (head.includes(":")) {
    const sub = head.split(":");
    if (sub[1] === "5") return [color256(Number(sub[2])), 1];
    if (sub[1] === "2") {
      const rgb = sub.length >= 6 ? sub.slice(3, 6) : sub.slice(2, 5);
      return [rgbColor(rgb), 1];
    }
    return [undefined, 1];
  }
  const mode = params[i + 1];
  if (mode === "5") return [color256(Number(params[i + 2])), 3];
  if (mode === "2") return [rgbColor(params.slice(i + 2, i + 5)), 5];
  return [undefined, 1];
}

function rgbColor(parts: string[]): string | undefined {
  if (parts.length < 3) return undefined;
  const [r, g, b] = parts.map((p) => Number(p || 0));
  if (![r, g, b].every((v) => Number.isInteger(v) && v! >= 0 && v! <= 255)) return undefined;
  return `rgb(${r}, ${g}, ${b})`;
}

function applySgr(style: AnsiStyle, paramStr: string): AnsiStyle {
  const params = paramStr === "" ? ["0"] : paramStr.split(";");
  const next: AnsiStyle = { ...style };
  for (let i = 0; i < params.length; i++) {
    const raw = params[i]!;
    const code = raw.includes(":") ? Number(raw.split(":")[0]) : Number(raw || 0);
    if (code === 38 || code === 48) {
      const [c, used] = extendedColor(params, i);
      if (code === 38) next.fg = c;
      else next.bg = c;
      i += used - 1;
      continue;
    }
    switch (true) {
      case code === 0:
        for (const k of Object.keys(next)) delete next[k as keyof AnsiStyle];
        break;
      case code === 1:
        next.bold = true;
        break;
      case code === 2:
        next.dim = true;
        break;
      case code === 3:
        next.italic = true;
        break;
      case code === 4:
        next.underline = true;
        break;
      case code === 7:
        next.inverse = true;
        break;
      case code === 9:
        next.strike = true;
        break;
      case code === 22:
        delete next.bold;
        delete next.dim;
        break;
      case code === 23:
        delete next.italic;
        break;
      case code === 24:
        delete next.underline;
        break;
      case code === 27:
        delete next.inverse;
        break;
      case code === 29:
        delete next.strike;
        break;
      case code >= 30 && code <= 37:
        next.fg = basicColor(code - 30, false);
        break;
      case code === 39:
        delete next.fg;
        break;
      case code >= 40 && code <= 47:
        next.bg = basicColor(code - 40, false);
        break;
      case code === 49:
        delete next.bg;
        break;
      case code >= 90 && code <= 97:
        next.fg = basicColor(code - 90, true);
        break;
      case code >= 100 && code <= 107:
        next.bg = basicColor(code - 100, true);
        break;
      default:
        break;
    }
  }
  for (const k of Object.keys(next) as (keyof AnsiStyle)[]) if (next[k] === undefined) delete next[k];
  return next;
}

export function parseAnsi(input: string): AnsiSegment[] {
  const segments: AnsiSegment[] = [];
  let style: AnsiStyle = {};
  let text = "";

  const flush = () => {
    if (!text) return;
    const s = isEmptyStyle(style) ? undefined : style;
    const last = segments[segments.length - 1];
    if (last && sameStyle(last.style, s)) last.text += text;
    else segments.push(s ? { text, style: s } : { text });
    text = "";
  };

  let i = 0;
  while (i < input.length) {
    const c = input.charCodeAt(i);
    if (c === 0x1b) {
      const next = input.charCodeAt(i + 1);
      if (next === 0x5b /* [ */) {
        let j = i + 2;
        while (j < input.length) {
          const ch = input.charCodeAt(j);
          if (ch >= 0x40 && ch <= 0x7e) break;
          j++;
        }
        if (j < input.length && input.charCodeAt(j) === 0x6d /* m */) {
          flush();
          style = applySgr(style, input.slice(i + 2, j));
        }
        i = j + 1;
        continue;
      }
      if (next === 0x5d /* ] */) {
        let j = i + 2;
        while (j < input.length) {
          const ch = input.charCodeAt(j);
          if (ch === 0x07) {
            j++;
            break;
          }
          if (ch === 0x1b && input.charCodeAt(j + 1) === 0x5c) {
            j += 2;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      i += Number.isNaN(next) ? 1 : 2;
      continue;
    }
    if (c >= 0x20 || c === 0x09) text += input[i];
    i++;
  }
  flush();
  return segments;
}

/** Plain text of a line (for search, filtering and copy). */
export function stripAnsi(input: string): string {
  let out = "";
  for (const seg of parseAnsi(input)) out += seg.text;
  return out;
}

export function styleToCss(style: AnsiStyle): JSX.CSSProperties {
  const css: JSX.CSSProperties = {};
  let fg = style.fg;
  let bg = style.bg;
  if (style.inverse) {
    fg = style.bg ?? "var(--background)";
    bg = style.fg ?? "var(--foreground)";
  }
  if (fg) css.color = fg;
  if (bg) css["background-color"] = bg;
  if (style.bold) css["font-weight"] = "600";
  if (style.dim) css.opacity = "0.65";
  if (style.italic) css["font-style"] = "italic";
  const deco = [style.underline && "underline", style.strike && "line-through"].filter(Boolean);
  if (deco.length) css["text-decoration"] = deco.join(" ");
  return css;
}
