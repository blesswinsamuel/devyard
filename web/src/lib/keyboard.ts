export const isMac =
  typeof navigator !== "undefined" && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent);

/**
 * Shortcut syntax: "+"-separated modifiers followed by a key, e.g. "r",
 * "shift+r", "mod+k" (mod = ⌘ on macOS, Ctrl elsewhere), "?".
 * Keys compare against KeyboardEvent.key, case-insensitively for letters.
 */
export interface ParsedShortcut {
  key: string;
  mod: boolean;
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
}

function parseShortcut(shortcut: string): ParsedShortcut {
  const parts = shortcut.split("+");
  const key = parts.pop() || "+";
  const mods = new Set(parts.map((p) => p.toLowerCase()));
  return {
    key: key.length === 1 ? key.toLowerCase() : key,
    mod: mods.has("mod"),
    ctrl: mods.has("ctrl"),
    alt: mods.has("alt"),
    shift: mods.has("shift"),
  };
}

type KeyEventLike = Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">;

export function matchShortcut(e: KeyEventLike, shortcut: string, mac = isMac): boolean {
  const s = parseShortcut(shortcut);
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  if (key !== s.key) return false;
  const wantMeta = s.mod && mac;
  const wantCtrl = s.ctrl || (s.mod && !mac);
  if (e.metaKey !== wantMeta || e.ctrlKey !== wantCtrl || e.altKey !== s.alt) return false;
  // Symbols like "?" already imply shift; only enforce shift for letters/named keys.
  const symbol = s.key.length === 1 && !/[a-z0-9]/.test(s.key);
  return symbol || e.shiftKey === s.shift;
}

/** True when the event target consumes typing (inputs, editors, terminals). */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  if (target.closest(".xterm")) return true;
  if (target instanceof HTMLElement && target.isContentEditable) return true;
  const tag = target.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    const type = (target as HTMLInputElement).type;
    return !["checkbox", "radio", "button", "submit", "reset", "range", "color"].includes(type);
  }
  return false;
}

/** Human rendering of a shortcut, split into key caps. */
export function shortcutKeys(shortcut: string, mac = isMac): string[] {
  const s = parseShortcut(shortcut);
  const keys: string[] = [];
  if (s.mod) keys.push(mac ? "⌘" : "Ctrl");
  if (s.ctrl) keys.push(mac ? "⌃" : "Ctrl");
  if (s.alt) keys.push(mac ? "⌥" : "Alt");
  if (s.shift) keys.push(mac ? "⇧" : "Shift");
  const named: Record<string, string> = { Enter: "↵", Escape: "Esc", ArrowUp: "↑", ArrowDown: "↓" };
  keys.push(named[s.key] ?? (s.key.length === 1 ? s.key.toUpperCase() : s.key));
  return keys;
}
