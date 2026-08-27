import { For } from "solid-js";
import { AlertTriangle, CheckCircle2, Info, X } from "lucide-solid";
import { dismissToast, toasts } from "~/stores/app";
import { Button } from "~/components/ui/button";

const ICONS = {
  error: AlertTriangle,
  success: CheckCircle2,
  info: Info,
};

export function Toasts() {
  return (
    <div class="pointer-events-none fixed right-4 top-4 z-[120] flex w-84 max-w-[calc(100vw-2rem)] flex-col gap-2">
      <For each={toasts()}>
        {(t) => {
          const Icon = ICONS[t.kind];
          return (
            <div
              class="pointer-events-auto flex items-start gap-2.5 rounded-lg border bg-popover px-3 py-2.5 text-[13px] shadow-md animate-toast-in"
              classList={{
                "border-destructive/40": t.kind === "error",
                "border-success/40": t.kind === "success",
                "border-border": t.kind === "info",
              }}
            >
              <Icon
                class="mt-0.5 size-4 shrink-0"
                classList={{
                  "text-destructive": t.kind === "error",
                  "text-success": t.kind === "success",
                  "text-info": t.kind === "info",
                }}
              />
              <span class="min-w-0 flex-1 break-words leading-snug">{t.message}</span>
              <Button
                variant="ghost"
                size="icon-sm"
                class="-m-1 size-6 shrink-0 text-muted-foreground"
                onClick={() => dismissToast(t.id)}
                aria-label="Dismiss"
              >
                <X class="size-3.5" />
              </Button>
            </div>
          );
        }}
      </For>
    </div>
  );
}
