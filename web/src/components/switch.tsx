import { Switch as SwitchPrimitive } from "~/components/ui/switch";
import { cn } from "~/lib/utils";

/**
 * The generated switch styles its unchecked track via `data-unchecked`,
 * which Kobalte never sets; style "not checked" instead.
 */
export function Switch(props: {
  id?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  class?: string;
}) {
  return (
    <SwitchPrimitive
      id={props.id}
      checked={props.checked}
      onChange={props.onChange}
      disabled={props.disabled}
      class={cn("not-data-checked:bg-input dark:not-data-checked:bg-input/80", props.class)}
    />
  );
}
