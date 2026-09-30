import { createSignal } from "solid-js";
import { createPersistedSignal, isBoolean } from "~/lib/persistence";
import type { ConfirmSpec } from "~/data/actions";
import type { TaskEntity } from "~/data/entities";

/** App-wide overlay state (palette, dialogs, sidebar). */
export const [paletteOpen, setPaletteOpen] = createSignal(false);
export const [shortcutsOpen, setShortcutsOpen] = createSignal(false);
export const [addProjectOpen, setAddProjectOpen] = createSignal(false);
export const [mobileNavOpen, setMobileNavOpen] = createSignal(false);
/** Project whose git commit message box should take focus once shown. */
export const [gitCommitFocus, setGitCommitFocus] = createSignal<string | null>(null);
export const [sidebarCollapsed, setSidebarCollapsed] = createPersistedSignal("sidebar.collapsed", false, isBoolean);

export interface ConfirmRequest extends ConfirmSpec {
  destructive: boolean;
  resolve: (ok: boolean) => void;
}

export const [confirmRequest, setConfirmRequest] = createSignal<ConfirmRequest | null>(null);

/** Opens the shared AlertDialog; resolves true when confirmed. */
export function confirm(spec: ConfirmSpec & { destructive?: boolean }): Promise<boolean> {
  confirmRequest()?.resolve(false);
  return new Promise((resolve) => setConfirmRequest({ destructive: true, ...spec, resolve }));
}

export interface ArgsRequest {
  task: TaskEntity;
  resolve: (args: string[] | null) => void;
}

export const [argsRequest, setArgsRequest] = createSignal<ArgsRequest | null>(null);

export function promptTaskArgs(task: TaskEntity): Promise<string[] | null> {
  argsRequest()?.resolve(null);
  return new Promise((resolve) => setArgsRequest({ task, resolve }));
}
