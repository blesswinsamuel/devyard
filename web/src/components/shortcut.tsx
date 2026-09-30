import { For } from "solid-js";
import { Kbd, KbdGroup } from "~/components/ui/kbd";
import { shortcutKeys } from "~/lib/keyboard";

export function Shortcut(props: { keys: string; class?: string }) {
  return (
    <KbdGroup class={props.class}>
      <For each={shortcutKeys(props.keys)}>{(k) => <Kbd>{k}</Kbd>}</For>
    </KbdGroup>
  );
}
