import { For, type JSX } from "solid-js";
import { parseAnsi, styleToCss, type AnsiSegment } from "~/lib/ansi";
import { linkify } from "~/lib/linkify";
import type { LogEntry } from "~/data/logs";

const segmentCache = new WeakMap<LogEntry, AnsiSegment[]>();

function segmentsOf(e: LogEntry): AnsiSegment[] {
  let segs = segmentCache.get(e);
  if (!segs) {
    segs = parseAnsi(e.text);
    segmentCache.set(e, segs);
  }
  return segs;
}

/** Splits `text` on highlight matches into plain strings and <mark>s. */
function highlighted(text: string, re: RegExp | null, current: boolean): JSX.Element {
  if (!re) return text;
  re.lastIndex = 0;
  const out: JSX.Element[] = [];
  let last = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m[0] === "") {
      re.lastIndex++;
      continue;
    }
    if (m.index > last) out.push(text.slice(last, m.index));
    out.push(
      <mark class={current ? "rounded-xs bg-log-match-current text-black" : "rounded-xs bg-log-match text-inherit"}>
        {m[0]}
      </mark>,
    );
    last = m.index + m[0].length;
  }
  if (!out.length) return text;
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/**
 * One log line's text: ANSI SGR styling, linkified URLs and search
 * highlights. Rendered once per entry (rows are recreated when the entry at
 * their index changes).
 */
export function LogText(props: { entry: LogEntry; highlight: RegExp | null; current: boolean }) {
  return (
    <For each={segmentsOf(props.entry)}>
      {(seg) => {
        const content = (
          <For each={linkify(seg.text)}>
            {(part) =>
              part.href ? (
                <a
                  href={part.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="underline decoration-dotted underline-offset-2 hover:decoration-solid"
                >
                  {highlighted(part.text, props.highlight, props.current)}
                </a>
              ) : (
                highlighted(part.text, props.highlight, props.current)
              )
            }
          </For>
        );
        return seg.style ? <span style={styleToCss(seg.style)}>{content}</span> : content;
      }}
    </For>
  );
}
