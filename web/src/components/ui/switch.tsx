import { splitProps, type ComponentProps } from "solid-js";
import { Switch as SwitchPrimitive } from "@kobalte/core";
import { cn } from "~/lib/utils";

export function Switch(props: ComponentProps<typeof SwitchPrimitive.Root>) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <SwitchPrimitive.Root
      class={cn(
        "inline-flex h-5 w-9 shrink-0 cursor-pointer items-center border border-input transition-colors data-[checked]:border-primary data-[checked]:bg-primary data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50",
        local.class
      )}
      {...rest}
    >
      <SwitchPrimitive.Input />
      <SwitchPrimitive.Control>
        <SwitchPrimitive.Thumb
          class={cn(
            "block size-4 bg-muted-foreground shadow-sm transition-transform data-[checked]:translate-x-4 data-[checked]:bg-primary-foreground"
          )}
        />
      </SwitchPrimitive.Control>
    </SwitchPrimitive.Root>
  );
}
