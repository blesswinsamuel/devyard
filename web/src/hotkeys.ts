import {
  actionProject,
  actionService,
  closeHelp,
  commitKeyboardCursor,
  killService,
  moveKeyboardCursor,
  moveKeyboardCursorToEnd,
  navigateKeyboardHorizontal,
  projects,
  restartService,
  showHelp,
  startProject,
  stopProject,
  stopService,
  toggleHelp,
} from "./store";

function isXtermTextarea(el: EventTarget | null): boolean {
  return el instanceof HTMLElement && el.classList.contains("xterm-helper-textarea");
}

function isEditableTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

function isMenuTarget(el: EventTarget | null): boolean {
  if (!(el instanceof Element)) return false;
  return Boolean(el.closest('[role="menu"], [role="listbox"], [data-kbd-ignore]'));
}

function blurXtermAndFocusSidebar() {
  const active = document.activeElement;
  if (active instanceof HTMLElement) active.blur();
  const nav = document.querySelector<HTMLElement>("[data-sidebar-nav]");
  nav?.focus({ preventScroll: true });
}

function scrollCursorIntoView() {
  // Defer so Solid can paint the new cursor highlight first.
  requestAnimationFrame(() => {
    const el = document.querySelector<HTMLElement>("[data-kbd-cursor]");
    el?.scrollIntoView({ block: "nearest" });
  });
}

function handleKeyDown(e: KeyboardEvent) {
  if (e.defaultPrevented) return;
  if (e.metaKey || e.ctrlKey || e.altKey) return;

  const target = e.target;
  const inXterm = isXtermTextarea(target);
  const inEditable = isEditableTarget(target) && !inXterm;
  const inMenu = isMenuTarget(target);

  // Esc always wins: close help, leave the terminal, or blur inputs.
  if (e.key === "Escape") {
    if (showHelp()) {
      e.preventDefault();
      closeHelp();
      return;
    }
    if (inXterm || inEditable) {
      e.preventDefault();
      blurXtermAndFocusSidebar();
      return;
    }
    return;
  }

  // While xterm / form fields / menus have focus, leave keys alone so
  // future interactive terminal sessions (and Kobalte menus) keep working.
  if (inXterm || inEditable || inMenu) return;

  if (showHelp()) {
    if (e.key === "?" || e.key === "Escape") {
      e.preventDefault();
      closeHelp();
    }
    // Swallow other keys while help is open (matches TUI).
    e.preventDefault();
    return;
  }

  switch (e.key) {
    case "ArrowUp":
      e.preventDefault();
      moveKeyboardCursor(-1);
      scrollCursorIntoView();
      break;
    case "ArrowDown":
      e.preventDefault();
      moveKeyboardCursor(1);
      scrollCursorIntoView();
      break;
    case "ArrowLeft":
      e.preventDefault();
      navigateKeyboardHorizontal("left");
      scrollCursorIntoView();
      break;
    case "ArrowRight":
      e.preventDefault();
      navigateKeyboardHorizontal("right");
      scrollCursorIntoView();
      break;
    case "Home":
      e.preventDefault();
      moveKeyboardCursorToEnd(false);
      scrollCursorIntoView();
      break;
    case "End":
      e.preventDefault();
      moveKeyboardCursorToEnd(true);
      scrollCursorIntoView();
      break;
    case "Enter":
      e.preventDefault();
      commitKeyboardCursor();
      break;
    case "r":
    case "R": {
      const t = actionService();
      if (!t) return;
      e.preventDefault();
      restartService(t.project, t.service);
      break;
    }
    case "s":
    case "S": {
      const t = actionService();
      if (!t) return;
      e.preventDefault();
      stopService(t.project, t.service);
      break;
    }
    case "k":
    case "K": {
      const t = actionService();
      if (!t) return;
      e.preventDefault();
      killService(t.project, t.service);
      break;
    }
    case "u":
    case "U": {
      const project = actionProject();
      if (!project) return;
      e.preventDefault();
      startProject(project);
      break;
    }
    case "d":
    case "D": {
      const project = actionProject();
      if (!project) return;
      e.preventDefault();
      stopProject(project);
      break;
    }
    case "?":
      e.preventDefault();
      toggleHelp();
      break;
    default:
      break;
  }
}

/** Install global keyboard shortcuts. Returns a disposer. */
export function setupHotkeys(): () => void {
  window.addEventListener("keydown", handleKeyDown);
  return () => window.removeEventListener("keydown", handleKeyDown);
}
