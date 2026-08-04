/**
 * Strip ANSI sequences that corrupt a log view (cursor movement, erase, OSC,
 * C0 controls) while preserving SGR (`…m`) so colors/bold stay intact.
 * Mirrors internal/ui.CleanLogLine.
 */
export function cleanLogLine(s: string): string {
  let out = "";
  let i = 0;
  while (i < s.length) {
    const c = s.charCodeAt(i);
    if (c === 0x1b) {
      const next = s.charCodeAt(i + 1);
      // CSI: ESC [ … final
      if (next === 0x5b /* [ */) {
        let j = i + 2;
        while (j < s.length) {
          const ch = s.charCodeAt(j);
          if (ch >= 0x40 && ch <= 0x7e) break;
          j++;
        }
        if (j < s.length && s.charCodeAt(j) === 0x6d /* m */) {
          out += s.slice(i, j + 1);
        }
        i = j < s.length ? j + 1 : s.length;
        continue;
      }
      // OSC: ESC ] … BEL or ST (ESC \)
      if (next === 0x5d /* ] */) {
        let j = i + 2;
        while (j < s.length) {
          const ch = s.charCodeAt(j);
          if (ch === 0x07) {
            j++;
            break;
          }
          if (ch === 0x1b && s.charCodeAt(j + 1) === 0x5c /* \ */) {
            j += 2;
            break;
          }
          j++;
        }
        i = j;
        continue;
      }
      // Other ESC sequences: drop ESC + following byte
      i += next !== undefined ? 2 : 1;
      continue;
    }
    // Printable (including space); drop C0 controls like CR/BS/TAB
    if (c >= 0x20) {
      out += s[i]!;
    }
    i++;
  }
  return out;
}
