import type { JSX } from "solid-js";
import { cn } from "~/lib/utils";

export function Logo(props: { class?: string }): JSX.Element {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden="true" class={cn("size-6 shrink-0", props.class)}>
      <rect width="32" height="32" rx="7" class="fill-primary" />
      <g class="stroke-primary-foreground" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
        <path d="M9 10.5 16 16 9 21.5" />
        <path d="M18.5 22h5" />
      </g>
    </svg>
  );
}
