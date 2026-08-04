import { splitProps, type ParentProps, type ComponentProps } from "solid-js";
import { DropdownMenu as DropdownMenuPrimitive } from "@kobalte/core";
import { cn } from "~/lib/utils";

export const DropdownMenu = DropdownMenuPrimitive.Root;
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger;
export const DropdownMenuGroup = DropdownMenuPrimitive.Group;

export function DropdownMenuContent(
  props: ParentProps<ComponentProps<typeof DropdownMenuPrimitive.Content>>
) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        class={cn(
          "z-50 min-w-32 border border-border bg-popover p-1 text-popover-foreground shadow-md",
          local.class
        )}
        {...rest}
      />
    </DropdownMenuPrimitive.Portal>
  );
}

export function DropdownMenuItem(
  props: ParentProps<ComponentProps<typeof DropdownMenuPrimitive.Item>>
) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <DropdownMenuPrimitive.Item
      class={cn(
        "relative flex cursor-pointer select-none items-center gap-2 px-2 py-1.5 text-sm outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50 focus:bg-accent focus:text-accent-foreground [&_svg]:size-3.5",
        local.class
      )}
      {...rest}
    />
  );
}

export function DropdownMenuSeparator(
  props: ParentProps<ComponentProps<typeof DropdownMenuPrimitive.Separator>>
) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <DropdownMenuPrimitive.Separator
      class={cn("-mx-1 my-1 h-px bg-border", local.class)}
      {...rest}
    />
  );
}
