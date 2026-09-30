import { actionForShortcut } from "~/data/actions";
import { isMac, isTypingTarget, matchShortcut } from "~/lib/keyboard";
import { contextFor, routeTarget, runAction, targetFromElement } from "./runtime";

/**
 * Global key handling for registry shortcuts. Single keys act on the focused
 * row/tree item (if any) or the current page's entity. Ignored while typing
 * in inputs or terminals and while a dialog/menu is open; modifier shortcuts
 * (⌘K, ⌘B, ⌘J) work in inputs too, but inside a terminal only ⌘ on macOS so
 * Ctrl-keys reach the shell.
 */
export function installShortcuts(): () => void {
  const onKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || e.isComposing || e.repeat) return;
    const target = e.target instanceof Element ? e.target : null;
    const mod = e.metaKey || e.ctrlKey;
    if (isTypingTarget(target)) {
      if (!mod) return;
      if (target?.closest(".xterm") && !(isMac && e.metaKey)) return;
    }
    if (target?.closest('[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]')) return;
    if (e.altKey) return;
    const actionTarget = targetFromElement(document.activeElement) ?? routeTarget();
    const ctx = contextFor(actionTarget);
    const action = actionForShortcut(ctx, (s) => matchShortcut(e, s));
    if (!action) return;
    e.preventDefault();
    void runAction(action, ctx.target);
  };
  window.addEventListener("keydown", onKey);
  return () => window.removeEventListener("keydown", onKey);
}
