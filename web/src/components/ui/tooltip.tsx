import { splitProps, type ComponentProps } from "solid-js";
import { Tooltip as TooltipPrimitive } from "@kobalte/core";
import { cn } from "~/lib/utils";

export function Tooltip(props: ComponentProps<typeof TooltipPrimitive.Root>) {
  return <TooltipPrimitive.Root openDelay={250} closeDelay={100} {...props} />;
}

export const TooltipTrigger = TooltipPrimitive.Trigger;

export function TooltipContent(
  props: ComponentProps<typeof TooltipPrimitive.Content>
) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        class={cn(
          "z-50 border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md",
          local.class
        )}
        {...rest}
      />
    </TooltipPrimitive.Portal>
  );
}
