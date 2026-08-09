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
    // Printable (including space); drop C0 controls like CR/BS except TAB (0x09)
    if (c >= 0x20 || c === 0x09) {
      out += s[i]!;
    }
    i++;
  }
  return out;
}

const ISO_TIMESTAMP_REGEX = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?) (.*)$/;

/**
 * Parses an ISO 8601/RFC3339 timestamp from the beginning of a raw log line,
 * formats it in local time (`HH:mm:ss`) wrapped in dim gray ANSI styling (`\x1b[90m`),
 * and cleans the remainder of the line.
 */
export function formatLogLine(rawLine: string): string {
  const match = rawLine.match(ISO_TIMESTAMP_REGEX);
  if (!match) {
    return cleanLogLine(rawLine);
  }
  const [, rawTs, content] = match;
  const date = new Date(rawTs!);
  if (isNaN(date.getTime())) {
    return cleanLogLine(rawLine);
  }
  const timeStr = date.toLocaleTimeString([], {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
  return `\x1b[90m${timeStr}\x1b[0m ${cleanLogLine(content!)}`;
}

