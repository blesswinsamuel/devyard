const SOURCE_COLORS = 8;

/** Stable colour for a log source / service name (one of the --source-N tokens). */
export function sourceColor(name: string): string {
  let h = 2166136261;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return `var(--source-${((h >>> 0) % SOURCE_COLORS) + 1})`;
}
