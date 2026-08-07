import { For, Show } from "solid-js";
import { showHelp, closeHelp } from "~/store";
import { Button } from "~/components/ui/button";

const SECTIONS: { title: string; rows: { keys: string; label: string }[] }[] = [
  {
    title: "Navigation",
    rows: [
      { keys: "↑ ↓", label: "Move cursor" },
      { keys: "← →", label: "Collapse / expand project" },
      { keys: "Enter", label: "Select project or service" },
      { keys: "Home / End", label: "First / last item" },
      { keys: "Esc", label: "Leave terminal · close help" },
    ],
  },
  {
    title: "Actions",
    rows: [
      { keys: "r", label: "Start / restart service" },
      { keys: "s", label: "Stop service" },
      { keys: "k", label: "Kill service (SIGKILL)" },
      { keys: "u", label: "Start project" },
      { keys: "d", label: "Stop project" },
      { keys: "p", label: "Toggle previous run's logs" },
    ],
  },
  {
    title: "Help",
    rows: [{ keys: "?", label: "Toggle this overlay" }],
  },
];

export function HelpOverlay() {
  return (
    <Show when={showHelp()}>
      <div
        class="fixed inset-0 z-[90] flex items-center justify-center bg-background/70 p-4 backdrop-blur-[2px]"
        onClick={() => closeHelp()}
        onKeyDown={(e) => {
          if (e.key === "Escape") closeHelp();
        }}
        role="presentation"
      >
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Keyboard shortcuts"
          class="w-full max-w-md border border-border bg-popover p-5 shadow-lg"
          onClick={(e) => e.stopPropagation()}
        >
          <div class="mb-4 flex items-baseline justify-between gap-3">
            <h2 class="text-sm font-semibold tracking-wide">Keyboard shortcuts</h2>
            <Button variant="ghost" size="sm" onClick={() => closeHelp()}>
              Esc
            </Button>
          </div>
          <div class="flex flex-col gap-4">
            <For each={SECTIONS}>
              {(section) => (
                <div>
                  <p class="mb-2 text-xs uppercase tracking-wider text-muted-foreground">
                    {section.title}
                  </p>
                  <ul class="flex flex-col gap-1.5">
                    <For each={section.rows}>
                      {(row) => (
                        <li class="flex items-center justify-between gap-4 text-sm">
                          <span class="text-muted-foreground">{row.label}</span>
                          <kbd class="shrink-0 border border-border bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                            {row.keys}
                          </kbd>
                        </li>
                      )}
                    </For>
                  </ul>
                </div>
              )}
            </For>
          </div>
          <p class="mt-5 text-xs leading-relaxed text-muted-foreground">
            Shortcuts are inactive while the log terminal is focused so interactive
            sessions can use the same keys. Press Esc to return focus to the sidebar.
          </p>
        </div>
      </div>
    </Show>
  );
}
