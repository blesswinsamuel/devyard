export type ListKeyResult = { index: number } | { open: true } | null;

/**
 * Keyboard movement through a list (commits, files): j/k and the arrow keys
 * step, Home/End jump, Enter opens the current item. `index` is -1 when
 * nothing in the list is selected; the first step then lands on the first item.
 */
export function listKey(key: string, index: number, length: number): ListKeyResult {
  if (length === 0) return null;
  const last = length - 1;
  switch (key) {
    case "j":
    case "ArrowDown":
      return { index: index < 0 ? 0 : Math.min(last, index + 1) };
    case "k":
    case "ArrowUp":
      return { index: index < 0 ? 0 : Math.max(0, index - 1) };
    case "Home":
      return { index: 0 };
    case "End":
      return { index: last };
    case "Enter":
      return index < 0 ? null : { open: true };
    default:
      return null;
  }
}

export interface CompareState {
  selected: string | undefined;
  base: string | undefined;
}

/**
 * What a ⌘/Ctrl-click on a commit does to a comparison: the first pick pins
 * the commit (and selects it); the next pick completes the pair while the
 * base sticks, and clicking the pinned base clears it again.
 */
export function nextCompareState(selected: string | undefined, base: string | undefined, clicked: string): CompareState {
  if (base === clicked) return { selected, base: undefined };
  if (base) return { selected: clicked, base };
  return { selected: clicked, base: clicked };
}
