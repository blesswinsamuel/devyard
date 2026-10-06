import { For, Show, type Component, type JSX } from "solid-js";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "~/components/ui/context-menu";

/**
 * One entry of a git row's right-click menu. These menus act on git objects
 * (branches, stashes, commits, files), which are query results rather than
 * bus entities, so they are plain items with handlers instead of registry
 * targets.
 */
export interface GitMenuItem {
  label: string;
  icon?: Component<{ class?: string }>;
  onSelect: () => void;
  destructive?: boolean;
  disabled?: boolean;
  /** Draws a separator above this item. */
  separated?: boolean;
}

/** Right-click menu over `children`, with the items of one git object. */
export function GitContextMenu(props: { class?: string; children: JSX.Element; items: GitMenuItem[] }) {
  return (
    <ContextMenu>
      <ContextMenuTrigger as="div" class={props.class}>
        {props.children}
      </ContextMenuTrigger>
      <ContextMenuContent class="min-w-40">
        <For each={props.items}>
          {(item) => {
            const Icon = item.icon;
            return (
              <>
                <Show when={item.separated}>
                  <ContextMenuSeparator />
                </Show>
                <ContextMenuItem
                  variant={item.destructive ? "destructive" : "default"}
                  disabled={item.disabled}
                  onSelect={() => item.onSelect()}
                >
                  {Icon ? <Icon /> : null}
                  <span>{item.label}</span>
                </ContextMenuItem>
              </>
            );
          }}
        </For>
      </ContextMenuContent>
    </ContextMenu>
  );
}
