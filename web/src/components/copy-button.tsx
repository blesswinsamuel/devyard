import { createSignal, Show } from "solid-js";
import { Check, Copy } from "lucide-solid";
import { toast } from "solid-sonner";
import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    toast.error("Couldn't copy to the clipboard");
    return false;
  }
}

export function CopyButton(props: { text: string; label?: string; class?: string }) {
  const [done, setDone] = createSignal(false);
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      class={cn("text-muted-foreground", props.class)}
      aria-label={props.label ?? "Copy"}
      title={props.label ?? "Copy"}
      onClick={async (e: MouseEvent) => {
        e.stopPropagation();
        if (await copyText(props.text)) {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        }
      }}
    >
      <Show when={done()} fallback={<Copy />}>
        <Check class="text-success" />
      </Show>
    </Button>
  );
}
