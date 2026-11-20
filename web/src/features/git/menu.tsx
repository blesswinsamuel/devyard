import { For, Show, type Component, type JSX } from "solid-js";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
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
  onSelect?: () => void;
  children?: GitMenuItem[];
  destructive?: boolean;
  disabled?: boolean;
  /** Draws a separator above this item. */
  separated?: boolean;
}

function MenuEntry(props: { item: GitMenuItem }) {
  const Icon = props.item.icon;
  return (
    <>
      <Show when={props.item.separated}>
        <ContextMenuSeparator />
      </Show>
      <Show
        when={props.item.children?.length}
        fallback={
          <ContextMenuItem
            variant={props.item.destructive ? "destructive" : "default"}
            disabled={props.item.disabled}
            onSelect={() => props.item.onSelect?.()}
          >
            {Icon ? <Icon /> : null}
            <span>{props.item.label}</span>
          </ContextMenuItem>
        }
      >
        <ContextMenuSub>
          <ContextMenuSubTrigger disabled={props.item.disabled}>
            {Icon ? <Icon /> : null}
            <span>{props.item.label}</span>
          </ContextMenuSubTrigger>
          <ContextMenuSubContent class="min-w-40">
            <For each={props.item.children}>{(child) => <MenuEntry item={child} />}</For>
          </ContextMenuSubContent>
        </ContextMenuSub>
      </Show>
    </>
  );
}

/** Right-click menu over `children`, with the items of one git object. */
export function GitContextMenu(props: { class?: string; children: JSX.Element; items: GitMenuItem[] }) {
  return (
    <ContextMenu>
      <ContextMenuTrigger as="div" class={props.class}>
        {props.children}
      </ContextMenuTrigger>
      <ContextMenuContent class="min-w-44">
        <For each={props.items}>{(item) => <MenuEntry item={item} />}</For>
      </ContextMenuContent>
    </ContextMenu>
  );
}
