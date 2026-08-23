import { splitProps, type ParentProps, type ComponentProps } from "solid-js";
import { Dialog as DialogPrimitive } from "@kobalte/core";
import { X } from "lucide-solid";
import { cn } from "~/lib/utils";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;

export function DialogContent(
  props: ParentProps<ComponentProps<typeof DialogPrimitive.Content> & { title?: string }>
) {
  const [local, rest] = splitProps(props, ["class", "title", "children"]);
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay
        class="fixed inset-0 z-[90] bg-black/45 backdrop-blur-[2px] animate-fade-in"
      />
      <DialogPrimitive.Content
        class={cn(
          "fixed left-1/2 top-1/2 z-[95] w-full max-w-md -translate-x-1/2 -translate-y-1/2",
          "rounded-xl border border-border-strong bg-popover text-popover-foreground shadow-lg",
          "focus:outline-none animate-fade-in",
          local.class
        )}
        {...rest}
      >
        <div class="flex items-center justify-between gap-3 border-b border-border px-5 py-3">
          <DialogPrimitive.Title class="text-sm font-semibold tracking-tight">
            {local.title ?? ""}
          </DialogPrimitive.Title>
          <DialogPrimitive.CloseButton
            class="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label="Close"
          >
            <X class="size-4" />
          </DialogPrimitive.CloseButton>
        </div>
        {local.children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}
