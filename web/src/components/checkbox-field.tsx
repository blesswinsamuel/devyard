import { createUniqueId } from "solid-js";
import { Checkbox } from "~/components/ui/checkbox";
import { cn } from "~/lib/utils";

/** Checkbox with a clickable label (the generated Checkbox renders no children). */
export function CheckboxField(props: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  class?: string;
}) {
  const id = createUniqueId();
  return (
    <div class={cn("flex items-center gap-2", props.disabled && "opacity-60", props.class)}>
      <Checkbox id={id} checked={props.checked} onChange={props.onChange} disabled={props.disabled} />
      <label for={id} class="cursor-pointer text-ui select-none">
        {props.label}
      </label>
    </div>
  );
}
