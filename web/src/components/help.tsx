import { For } from "solid-js";
import { closeHelp, showHelp } from "~/stores/app";
import { Dialog, DialogContent } from "~/components/ui/dialog";

const SECTIONS: { title: string; rows: { keys: string; label: string }[] }[] = [
  {
    title: "Navigation",
    rows: [
      { keys: "↑ ↓", label: "Move cursor" },
      { keys: "← →", label: "Collapse / expand project" },
      { keys: "Enter", label: "Select project or service" },
      { keys: "Home / End", label: "First / last item" },
      { keys: "Esc", label: "Leave terminal · close dialogs" },
    ],
  },
  {
    title: "Actions",
    rows: [
      { keys: "r", label: "Restart service" },
      { keys: "s", label: "Stop service" },
      { keys: "k k", label: "Kill service (press twice)" },
      { keys: "u", label: "Start project" },
      { keys: "d d", label: "Stop project (press twice)" },
      { keys: "p", label: "Toggle previous run's logs" },
    ],
  },
  {
    title: "General",
    rows: [
      { keys: "?", label: "Toggle this overlay" },
      { keys: "g", label: "Open git view for selected project" },
      { keys: "t", label: "Toggle terminal panel" },
    ],
  },
];

export function HelpOverlay() {
  return (
    <Dialog open={showHelp()} onOpenChange={(open) => !open && closeHelp()}>
      <DialogContent title="Keyboard shortcuts">
        <div class="flex flex-col gap-4 px-5 py-4">
          <For each={SECTIONS}>
            {(section) => (
              <div>
                <p class="mb-2 text-[10.5px] font-semibold uppercase tracking-wider text-muted-foreground">
                  {section.title}
                </p>
                <ul class="flex flex-col gap-1.5">
                  <For each={section.rows}>
                    {(row) => (
                      <li class="flex items-center justify-between gap-4 text-[13px]">
                        <span class="text-muted-foreground">{row.label}</span>
                        <kbd>{row.keys}</kbd>
                      </li>
                    )}
                  </For>
                </ul>
              </div>
            )}
          </For>
          <p class="mt-1 border-t pt-3 text-xs leading-relaxed text-muted-foreground">
            Shortcuts are inactive while a terminal or form has focus, so interactive
            sessions can use the same keys. Press Esc to return focus to the sidebar.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}
