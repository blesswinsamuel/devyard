import { splitProps, type ComponentProps } from "solid-js";
import { Separator as SeparatorPrimitive } from "@kobalte/core";
import { cn } from "~/lib/utils";

export function Separator(props: ComponentProps<typeof SeparatorPrimitive.Root>) {
  const [local, rest] = splitProps(props, ["class", "orientation"]);
  return (
    <SeparatorPrimitive.Root
      orientation={local.orientation}
      class={cn(
        "shrink-0 bg-border",
        local.orientation === "vertical" ? "h-full w-px" : "h-px w-full",
        local.class
      )}
      {...rest}
    />
  );
}
