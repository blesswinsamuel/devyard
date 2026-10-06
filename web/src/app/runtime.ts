import { createSignal } from "solid-js";
import { toast } from "solid-sonner";
import {
  actionTitle,
  resolveContext,
  targetKey,
  type Action,
  type ActionContext,
  type ActionEnv,
  type ActionTarget,
} from "~/data/actions";
import { api } from "~/data/client";
import { dockActions } from "~/data/dock";
import { entities } from "~/data/entities";
import { errorDescription, isCanceled } from "~/data/errors";
import { isPending, withPending } from "~/data/pending";
import { connection } from "~/data/sync";
import { isMobile } from "~/lib/media";
import { paths } from "~/lib/paths";
import { cycleTheme } from "~/lib/theme";
import {
  confirm,
  promptGitBranch,
  promptTaskArgs,
  setAddProjectOpen,
  setGitCommitFocus,
  setMobileNavOpen,
  setPaletteOpen,
  setShortcutsOpen,
  setSidebarCollapsed,
} from "./ui-state";

let navigateImpl: (path: string) => void = (path) => history.pushState(null, "", path);

/** Called once by the shell with the router's navigate. */
export function bindNavigate(fn: (path: string) => void): void {
  navigateImpl = fn;
}

/** The route's entity (set by the shell); the default target for shortcuts. */
export const [routeTarget, setRouteTarget] = createSignal<ActionTarget>({ kind: "app" });

const actionEnv: ActionEnv = {
  api,
  navigate: (path) => navigateImpl(path),
  currentPath: () => location.pathname,
  openTerminal: (project) => dockActions.openTerminal(project),
  attach: (kind, project, name) => dockActions.attach(kind, project, name),
  pinLogs: (project, service) =>
    dockActions.pinLogs(project, service ? [{ kind: "service", name: service }] : [], service ?? `${project} · all`),
  promptTaskArgs,
  promptGitBranch,
  openAddProject: () => setAddProjectOpen(true),
  openShortcuts: () => setShortcutsOpen(true),
  openPalette: () => setPaletteOpen(true),
  openGitCommit: (project) => {
    setGitCommitFocus(project);
    navigateImpl(paths.gitCommit(project, "WORKDIR"));
  },
  toggleSidebar: () => (isMobile() ? setMobileNavOpen((o) => !o) : setSidebarCollapsed((c) => !c)),
  toggleDock: () => dockActions.toggle(),
  cycleTheme,
  notify: (message, description) => void toast.success(message, { description }),
};

export const contextFor = (target: ActionTarget): ActionContext => resolveContext(target, entities.state);

const pendingKey = (action: Action, target: ActionTarget) => `${action.id}|${targetKey(target)}`;
export const actionPending = (action: Action, target: ActionTarget) => isPending(pendingKey(action, target));
export const actionBlocked = (action: Action) => !action.local && connection.state.phase !== "live";

/** Runs an action with confirmation, pending state and error toasts. */
export async function runAction(action: Action, target: ActionTarget): Promise<void> {
  const ctx = contextFor(target);
  if (!action.when(ctx)) return;
  if (actionBlocked(action)) {
    toast.error("Not connected to the daemon", { description: "Wait for the connection to come back and try again." });
    return;
  }
  if (action.confirm && !(await confirm({ ...action.confirm(ctx), destructive: !!action.destructive }))) return;
  const title = actionTitle(action, ctx);
  await withPending(pendingKey(action, target), async () => {
    try {
      await action.run(contextFor(target), actionEnv);
    } catch (err) {
      if (isCanceled(err)) return;
      toast.error(`${title} failed`, { description: errorDescription(err) });
    }
  });
}

// -----------------------------------------------------------------------------
// DOM targets: rows/tree items carry their entity so shortcuts and context
// menus act on the focused element rather than the page.
// -----------------------------------------------------------------------------

export function targetAttrs(target: ActionTarget): Record<string, string> {
  if (target.kind === "app") return { "data-target-kind": "app" };
  const attrs: Record<string, string> = { "data-target-kind": target.kind, "data-target-project": target.project };
  if (target.kind !== "project") attrs["data-target-name"] = target.name;
  return attrs;
}

export function targetFromElement(el: Element | null): ActionTarget | null {
  const node = el?.closest?.("[data-target-kind]");
  if (!node) return null;
  const kind = node.getAttribute("data-target-kind");
  const project = node.getAttribute("data-target-project") ?? "";
  const name = node.getAttribute("data-target-name") ?? "";
  if (kind === "project") return { kind, project };
  if (kind === "service" || kind === "task") return { kind, project, name };
  if (kind === "app") return { kind: "app" };
  return null;
}
