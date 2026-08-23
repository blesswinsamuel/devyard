import { splitProps, type ComponentProps } from "solid-js";
import { Switch as SwitchPrimitive } from "@kobalte/core";
import { cn } from "~/lib/utils";

export function Switch(props: ComponentProps<typeof SwitchPrimitive.Root>) {
  const [local, rest] = splitProps(props, ["class"]);
  return (
    <SwitchPrimitive.Root
      class={cn(
        "inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border border-input bg-muted transition-colors duration-100 data-[checked]:border-primary data-[checked]:bg-primary data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        local.class
      )}
      {...rest}
    >
      <SwitchPrimitive.Input />
      <SwitchPrimitive.Control class="flex h-full w-full items-center rounded-full px-0.5">
        <SwitchPrimitive.Thumb
          class={cn(
            "block size-3.5 rounded-full bg-white shadow-sm transition-transform duration-100 dark:bg-zinc-300 data-[checked]:translate-x-4 data-[checked]:bg-primary-foreground"
          )}
        />
      </SwitchPrimitive.Control>
    </SwitchPrimitive.Root>
  );
}
