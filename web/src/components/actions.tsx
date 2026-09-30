import { createMemo, For, Show, type JSX, type ValidComponent } from "solid-js";
import { Ellipsis } from "lucide-solid";
import { Button, type ButtonProps } from "~/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "~/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Spinner } from "~/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { actionsFor, getAction, type Action, type ActionTarget } from "~/data/actions";
import { shortcutKeys } from "~/lib/keyboard";
import { cn } from "~/lib/utils";
import { actionBlocked, actionPending, contextFor, runAction } from "~/app/runtime";

/**
 * Registry-driven controls. Every button/menu item resolves availability,
 * confirmation and pending state through the same action definitions.
 */

export function ActionButton(props: {
  id: string;
  target: ActionTarget;
  variant?: ButtonProps["variant"];
  size?: ButtonProps["size"];
  /** Icon-only with the label as tooltip/aria-label. */
  iconOnly?: boolean;
  label?: string;
  class?: string;
}) {
  const action = getAction(props.id);
  const ctx = () => contextFor(props.target);
  const available = () => action.when(ctx());
  const pending = () => actionPending(action, props.target);
  const label = () => props.label ?? action.label;
  const Icon = action.icon;
  const hint = () => (action.shortcut ? `${label()} (${shortcutKeys(action.shortcut).join("")})` : label());

  const button = () => (
    <Button
      variant={props.variant ?? (action.destructive ? "outline" : "outline")}
      size={props.size ?? (props.iconOnly ? "icon-sm" : "sm")}
      class={cn(action.destructive && "text-destructive hover:text-destructive", props.class)}
      disabled={pending() || actionBlocked(action)}
      aria-label={props.iconOnly ? label() : undefined}
      aria-busy={pending() || undefined}
      onClick={(e: MouseEvent) => {
        e.stopPropagation();
        void runAction(action, props.target);
      }}
    >
      <Show when={pending()} fallback={Icon ? <Icon /> : null}>
        <Spinner />
      </Show>
      <Show when={!props.iconOnly}>{label()}</Show>
    </Button>
  );

  return (
    <Show when={available()}>
      <Show when={props.iconOnly} fallback={button()}>
        <Tooltip>
          <TooltipTrigger as="span" class="inline-flex">
            {button()}
          </TooltipTrigger>
          <TooltipContent>{hint()}</TooltipContent>
        </Tooltip>
      </Show>
    </Show>
  );
}

/** Splits actions into registry groups for separators. */
function grouped(actions: Action[]): Action[][] {
  const groups: Action[][] = [];
  let last: string | undefined;
  for (const a of actions) {
    const key = `${a.group}${a.destructive ? "!" : ""}`;
    if (key !== last) groups.push([]);
    groups[groups.length - 1]!.push(a);
    last = key;
  }
  return groups;
}

function useActions(target: () => ActionTarget, opts: { inherit?: boolean; exclude?: string[] }) {
  return createMemo(() =>
    actionsFor(contextFor(target()), { inherit: opts.inherit }).filter((a) => !opts.exclude?.includes(a.id)),
  );
}

function ItemBody(props: { action: Action; target: ActionTarget; Shortcut: (p: { children: JSX.Element }) => JSX.Element }) {
  const Icon = props.action.icon;
  return (
    <>
      <Show when={actionPending(props.action, props.target)} fallback={Icon ? <Icon /> : null}>
        <Spinner />
      </Show>
      <span>{props.action.label}</span>
      <Show when={props.action.shortcut}>{(s) => <props.Shortcut>{shortcutKeys(s()).join("")}</props.Shortcut>}</Show>
    </>
  );
}

/** "…" dropdown with every available action for a target. */
export function ActionsDropdown(props: {
  target: ActionTarget;
  inherit?: boolean;
  exclude?: string[];
  label?: string;
  class?: string;
  size?: ButtonProps["size"];
}) {
  const actions = useActions(() => props.target, props);
  return (
    <Show when={actions().length}>
      <DropdownMenu>
        <DropdownMenuTrigger
          as={Button}
          variant="ghost"
          size={props.size ?? "icon-sm"}
          class={props.class}
          aria-label={props.label ?? "More actions"}
          onClick={(e: MouseEvent) => e.stopPropagation()}
        >
          <Ellipsis />
        </DropdownMenuTrigger>
        <DropdownMenuContent class="min-w-48">
          <For each={grouped(actions())}>
            {(group, i) => (
              <>
                <Show when={i() > 0}>
                  <DropdownMenuSeparator />
                </Show>
                <For each={group}>
                  {(action) => (
                    <DropdownMenuItem
                      variant={action.destructive ? "destructive" : "default"}
                      disabled={actionPending(action, props.target) || actionBlocked(action)}
                      onSelect={() => void runAction(action, props.target)}
                    >
                      <ItemBody action={action} target={props.target} Shortcut={DropdownMenuShortcut} />
                    </DropdownMenuItem>
                  )}
                </For>
              </>
            )}
          </For>
        </DropdownMenuContent>
      </DropdownMenu>
    </Show>
  );
}

/** Right-click menu over `children` for a target. */
export function ActionContextMenu(props: {
  target: ActionTarget;
  inherit?: boolean;
  children: JSX.Element;
  class?: string;
  /** Element/component rendered as the trigger (default div; e.g. TableRow). */
  as?: ValidComponent;
  /** Extra attributes for the trigger element (e.g. role/tabindex for tree items). */
  triggerProps?: Record<string, unknown>;
}) {
  const actions = useActions(() => props.target, { inherit: props.inherit ?? true });
  return (
    <ContextMenu>
      <ContextMenuTrigger as={props.as ?? "div"} class={props.class} {...props.triggerProps}>
        {props.children}
      </ContextMenuTrigger>
      <ContextMenuContent class="min-w-48">
        <For each={grouped(actions())}>
          {(group, i) => (
            <>
              <Show when={i() > 0}>
                <ContextMenuSeparator />
              </Show>
              <For each={group}>
                {(action) => (
                  <ContextMenuItem
                    variant={action.destructive ? "destructive" : "default"}
                    disabled={actionPending(action, props.target) || actionBlocked(action)}
                    onSelect={() => void runAction(action, props.target)}
                  >
                    <ItemBody action={action} target={props.target} Shortcut={ContextMenuShortcut} />
                  </ContextMenuItem>
                )}
              </For>
            </>
          )}
        </For>
      </ContextMenuContent>
    </ContextMenu>
  );
}
