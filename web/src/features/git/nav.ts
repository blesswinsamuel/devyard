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
