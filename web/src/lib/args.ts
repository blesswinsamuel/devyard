/**
 * Splits a command-line string into arguments the way a POSIX shell would for
 * plain words: whitespace separates, single quotes are literal, double quotes
 * allow \" and \\ escapes, and a backslash escapes the next character outside
 * quotes. Returns an error for unterminated quotes.
 */
export function splitArgs(input: string): { args: string[] } | { error: string } {
  const args: string[] = [];
  let current = "";
  let inWord = false;
  let quote: "'" | '"' | null = null;
  for (let i = 0; i < input.length; i++) {
    const c = input[i]!;
    if (quote === "'") {
      if (c === "'") quote = null;
      else current += c;
      continue;
    }
    if (quote === '"') {
      if (c === '"') quote = null;
      else if (c === "\\" && (input[i + 1] === '"' || input[i + 1] === "\\")) current += input[++i];
      else current += c;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      inWord = true;
    } else if (c === "\\" && i + 1 < input.length) {
      current += input[++i];
      inWord = true;
    } else if (/\s/.test(c)) {
      if (inWord) args.push(current);
      current = "";
      inWord = false;
    } else {
      current += c;
      inWord = true;
    }
  }
  if (quote) return { error: `Unterminated ${quote === "'" ? "single" : "double"} quote` };
  if (inWord) args.push(current);
  return { args };
}

/** Inverse of splitArgs for display: quotes arguments that need it. */
export function joinArgs(args: string[]): string {
  return args.map((a) => (a === "" || /[\s'"\\$`]/.test(a) ? `'${a.replace(/'/g, `'\\''`)}'` : a)).join(" ");
}
