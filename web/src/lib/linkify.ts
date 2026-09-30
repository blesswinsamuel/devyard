export interface TextPart {
  text: string;
  href?: string;
}

const URL_RE = /\bhttps?:\/\/[^\s<>"'`]+/g;
const TRAILING = /[.,;:!?'"]+$/;

/** Splits text into plain and URL parts. Trailing punctuation and unbalanced
 * closing parens/brackets are excluded from the link. */
export function linkify(text: string): TextPart[] {
  if (!text.includes("://")) return [{ text }];
  const parts: TextPart[] = [];
  let last = 0;
  for (const m of text.matchAll(URL_RE)) {
    let url = m[0];
    url = url.replace(TRAILING, "");
    for (const [open, close] of [
      ["(", ")"],
      ["[", "]"],
    ] as const) {
      while (url.endsWith(close) && count(url, close) > count(url, open)) url = url.slice(0, -1);
    }
    url = url.replace(TRAILING, "");
    const start = m.index!;
    if (start > last) parts.push({ text: text.slice(last, start) });
    parts.push({ text: url, href: url });
    last = start + url.length;
  }
  if (last < text.length) parts.push({ text: text.slice(last) });
  return parts;
}

function count(s: string, ch: string): number {
  let n = 0;
  for (const c of s) if (c === ch) n++;
  return n;
}
