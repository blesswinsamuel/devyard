import { toast } from "solid-sonner";
import { createSignal } from "solid-js";

// --- toasts ---------------------------------------------------------------

export type ToastKind = "error" | "info" | "success";

const TOAST_TTL = 5000;

const activeToastIds = new Map<string, string | number>();

/**
 * Pushes a toast, collapsing repeats of the same message into a fresh entry
 * (sonner can't re-arm a live toast) so a failing poll can't flood the screen.
 */
export function pushToast(message: string, kind: ToastKind = "error") {
  const previous = activeToastIds.get(message);
  if (previous !== undefined) {
    toast.dismiss(previous);
    activeToastIds.delete(message);
  }
  const id = toast[kind](message, {
    duration: TOAST_TTL,
    onDismiss: () => activeToastIds.delete(message),
    onAutoClose: () => activeToastIds.delete(message),
  });
  activeToastIds.set(message, id);
}

// --- overlays -------------------------------------------------------------

/** Mobile-only: whether the project sidebar drawer is slid in. */
const [sidebarOpen, setSidebarOpen] = createSignal(false);

export { sidebarOpen, setSidebarOpen };

const [showPortsDialog, setShowPortsDialog] = createSignal(false);
const [showAddProject, setShowAddProject] = createSignal(false);
const [showDaemonModal, setShowDaemonModal] = createSignal(false);
const [showSettingsModal, setShowSettingsModal] = createSignal(false);
const [showHelp, setShowHelp] = createSignal(false);

export {
  showPortsDialog,
  setShowPortsDialog,
  showAddProject,
  setShowAddProject,
  showDaemonModal,
  setShowDaemonModal,
  showSettingsModal,
  setShowSettingsModal,
  showHelp,
  setShowHelp,
};

/** Opens the global ports & URLs dialog. */
export function openPortsDialog() {
  setShowPortsDialog(true);
}

export function toggleHelp() {
  setShowHelp((v) => !v);
}

export function closeHelp() {
  setShowHelp(false);
}

export function anyOverlayOpen(): boolean {
  return showPortsDialog() || showAddProject() || showDaemonModal() || showSettingsModal() || showHelp();
}
