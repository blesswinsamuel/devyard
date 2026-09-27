import {
  actionProject,
  actionService,
  commitKeyboardCursor,
  moveKeyboardCursor,
  moveKeyboardCursorToEnd,
  navigateKeyboardHorizontal,
  openGitTab,
  previousTarget,
} from "~/stores/nav";
import { killService, restartService, startProject, stopProject, stopService } from "~/stores/data";
import { anyOverlayOpen, closeHelp, pushToast, showHelp, toggleHelp } from "~/stores/app";
import { focusedPaneActiveTab, openTerminalTab, togglePreviousLogs, serviceLogTabId, taskLogTabId } from "~/stores/workspace";

function activeTabIsLog(): boolean {
  const tab = focusedPaneActiveTab();
  return tab?.kind === "log-service" || tab?.kind === "log-task";
}

/**
 * Destructive keys require a second press within CONFIRM_WINDOW to fire.
 * The first press raises an explanatory toast instead of acting.
 */
const CONFIRM_WINDOW = 2000;
let pendingConfirm: { key: string; label: string; run: () => void; expires: number } | null = null;

function confirmable(key: string, label: string, toastText: string, run: () => void): void {
  const now = Date.now();
  if (pendingConfirm && pendingConfirm.key === key && now < pendingConfirm.expires) {
    pendingConfirm = null;
    run();
    return;
  }
  pendingConfirm = { key, label, run, expires: now + CONFIRM_WINDOW };
  pushToast(toastText, "info");
}

function isXtermTarget(el: EventTarget | null): boolean {
  return el instanceof Element && (el.classList.contains("xterm-helper-textarea") || !!el.closest(".xterm"));
}

function isEditableTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

function isMenuTarget(el: EventTarget | null): boolean {
  if (!(el instanceof Element)) return false;
  return !!el.closest('[role="menu"], [role="listbox"], [role="dialog"], [data-kbd-ignore]');
}

function focusSidebar() {
  const active = document.activeElement;
  if (active instanceof HTMLElement) active.blur();
  document.querySelector<HTMLElement>("[data-sidebar-nav]")?.focus({ preventScroll: true });
}

function scrollCursorIntoView() {
  requestAnimationFrame(() => {
    document
      .querySelector<HTMLElement>("[data-kbd-cursor]")
      ?.scrollIntoView({ block: "nearest" });
  });
}

async function handleKeyDown(e: KeyboardEvent): Promise<void> {
  if (e.defaultPrevented) return;
  if (e.metaKey || e.ctrlKey || e.altKey) return;

  const target = e.target;
  const inXterm = isXtermTarget(target);
  const inEditable = isEditableTarget(target) && !inXterm;
  const inMenu = isMenuTarget(target);

  // Escape always wins.
  if (e.key === "Escape") {
    if (anyOverlayOpen()) {
      e.preventDefault();
      closeHelp();
      return;
    }
    if (inXterm || inEditable) {
      e.preventDefault();
      focusSidebar();
    }
    return;
  }

  // Leave interactive surfaces alone.
  if (inXterm || inEditable || inMenu) return;

  if (showHelp()) {
    e.preventDefault();
    if (e.key === "?" ) toggleHelp();
    else closeHelp();
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
    case "?":
      e.preventDefault();
      toggleHelp();
      break;
    case "g":
    case "G": {
      const project = actionProject();
      if (!project) return;
      e.preventDefault();
      openGitTab(project);
      break;
    }
    case "t":
    case "T": {
      const project = actionProject();
      if (!project) return;
      e.preventDefault();
      openTerminalTab(project);
      break;
    }
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
      confirmable("k", `kill ${t.service}`, `Press k again within 2s to kill '${t.service}'`, () =>
        killService(t.project, t.service)
      );
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
      confirmable("d", `stop ${project}`, `Press d again within 2s to stop project '${project}'`, () =>
        stopProject(project)
      );
      break;
    }
    case "p":
    case "P": {
      // Toggle previous-run mode on the focused pane's active log tab; when the
      // cursor sits on a service/task whose tab is open elsewhere, target that.
      const active = focusedPaneActiveTab();
      if (activeTabIsLog()) {
        e.preventDefault();
        togglePreviousLogs(active!.id);
        break;
      }
      const t = previousTarget();
      if (!t || t.kind === "project") return;
      e.preventDefault();
      const id =
        t.kind === "service"
          ? serviceLogTabId(t.project, t.service)
          : taskLogTabId(t.project, t.task);
      togglePreviousLogs(id);
      break;
    }
    default:
      break;
  }
}

/** Installs global shortcuts plus the pointer-focus guard. Returns a disposer. */
export function setupHotkeys(): () => void {
  window.addEventListener("keydown", handleKeyDown);

  // After mouse clicks on buttons/links, drop focus so single-letter shortcuts
  // keep working (keyboard users are unaffected — Tab focus still lands).
  const onPointerUp = (e: PointerEvent) => {
    if (e.pointerType !== "mouse") return;
    const el = document.activeElement;
    if (el instanceof HTMLElement && (el.tagName === "BUTTON" || el.tagName === "A")) {
      el.blur();
    }
  };
  window.addEventListener("pointerup", onPointerUp);

  return () => {
    window.removeEventListener("keydown", handleKeyDown);
    window.removeEventListener("pointerup", onPointerUp);
  };
}
