import { For, type JSX, Show, createSignal } from "solid-js";
import { X } from "lucide-solid";
import { cn } from "~/lib/utils";

/**
 * The one tab strip used everywhere: workspace panes (log/git/terminal tabs)
 * and any future closable tab surface. Handles selection, close buttons,
 * drag-to-reorder within the strip, and drag-to-move into another strip.
 */

export interface TabStripItem {
  id: string;
  title: string;
  /** Dimmed prefix shown before the title (e.g. cross-project name). */
  prefix?: string;
  /** Leading icon/status dot. */
  icon?: JSX.Element;
  tooltip?: string;
}

export const TAB_DRAG_MIME = "application/x-devyard-tab";

export interface TabDragPayload {
  tabId: string;
  fromPaneId: string;
}

export function setTabDragData(e: DragEvent, payload: TabDragPayload) {
  e.dataTransfer?.setData(TAB_DRAG_MIME, JSON.stringify(payload));
}

export function readTabDragData(e: DragEvent): TabDragPayload | null {
  const raw = e.dataTransfer?.getData(TAB_DRAG_MIME);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as TabDragPayload;
  } catch {
    return null;
  }
}

export function TabStrip(props: {
  items: TabStripItem[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onClose?: (id: string) => void;
  onReorder?: (fromIndex: number, toIndex: number) => void;
  /** Cross-strip move; target index is append unless given. */
  onMoveTo?: (payload: TabDragPayload, index: number) => void;
  /** Element rendered after the last tab (add button, pane controls…). */
  trailing?: JSX.Element;
  class?: string;
}) {
  const [dragOverIndex, setDragOverIndex] = createSignal<number | null>(null);

  const indexFromPoint = (clientX: number): number => {
    const strip = stripRef;
    if (!strip) return props.items.length;
    const children = Array.from(strip.querySelectorAll<HTMLElement>("[data-tab-id]"));
    for (let i = 0; i < children.length; i++) {
      const rect = children[i]!.getBoundingClientRect();
      if (clientX < rect.left + rect.width / 2) return i;
    }
    return children.length;
  };

  const handleDragOver = (e: DragEvent) => {
    if (!e.dataTransfer?.types.includes(TAB_DRAG_MIME)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    setDragOverIndex(indexFromPoint(e.clientX));
  };

  const handleDrop = (e: DragEvent) => {
    const payload = readTabDragData(e);
    setDragOverIndex(null);
    if (!payload) return;
    e.preventDefault();
    e.stopPropagation();
    const index = indexFromPoint(e.clientX);
    const fromIdx = props.items.findIndex((t) => t.id === payload.tabId);
    if (fromIdx === -1) {
      props.onMoveTo?.(payload, index);
      return;
    }
    // Same strip: reorder, adjusting for the removal of the dragged tab.
    const adjusted = index > fromIdx ? index - 1 : index;
    if (adjusted !== fromIdx) props.onReorder?.(fromIdx, adjusted);
    else props.onReorder?.(fromIdx, fromIdx);
  };

  let stripRef: HTMLDivElement | undefined;

  return (
    <div
      ref={stripRef}
      class={cn("flex min-w-0 items-center gap-1 overflow-x-auto", props.class)}
      onDragOver={handleDragOver}
      onDragLeave={(e) => {
        if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) setDragOverIndex(null);
      }}
      onDrop={handleDrop}
    >
      <For each={props.items}>
        {(item, index) => {
          const isActive = () => props.activeId === item.id;
          return (
            <div
              data-tab-id={item.id}
              class={cn(
                "group relative flex h-6 shrink-0 items-center rounded-md text-xs transition-colors",
                "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                isActive()
                  ? "bg-accent font-medium text-accent-foreground"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
                dragOverIndex() === index() && "before:absolute before:inset-y-0 before:-left-1 before:w-0.5 before:rounded-full before:bg-primary"
              )}
            >
              <button
                type="button"
                onClick={() => props.onSelect(item.id)}
                title={item.tooltip}
                class="flex min-w-0 cursor-grab items-center gap-1.5 px-2 py-0.5 focus-visible:outline-none active:cursor-grabbing"
                draggable="true"
                onDragStart={(e) => setTabDragData(e, { tabId: item.id, fromPaneId: "" })}
              >
                <Show when={item.icon}>{item.icon}</Show>
                <span class="truncate">
                  <Show when={item.prefix}>
                    <span class="text-muted-foreground/70">{item.prefix}</span>
                  </Show>
                  {item.title}
                </span>
              </button>
              <Show when={props.onClose}>
                <button
                  type="button"
                  aria-label={`Close ${item.title}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    props.onClose?.(item.id);
                  }}
                  class={cn(
                    "mr-0.5 flex size-4 items-center justify-center rounded-full text-muted-foreground/60 transition-colors hover:bg-muted hover:text-foreground",
                    !isActive() && "opacity-0 group-hover:opacity-100"
                  )}
                >
                  <X class="size-3" />
                </button>
              </Show>
            </div>
          );
        }}
      </For>
      {props.trailing}
    </div>
  );
}
