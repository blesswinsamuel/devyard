import { For } from "solid-js";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Shortcut } from "~/components/shortcut";
import { ACTIONS, type ActionScope } from "~/data/actions";
import { shortcutsOpen, setShortcutsOpen } from "~/app/ui-state";

const SCOPE_TITLES: Record<ActionScope, string> = {
  app: "Anywhere",
  project: "Project (page or focused row)",
  service: "Service (page or focused row)",
  task: "Task (page or focused row)",
};

const EXTRA: { title: string; items: { keys: string[]; label: string }[] }[] = [
  {
    title: "Logs",
    items: [
      { keys: ["/", "mod+f"], label: "Search (log view focused)" },
      { keys: ["Enter", "shift+Enter"], label: "Next / previous match" },
      { keys: ["End"], label: "Jump to latest" },
    ],
  },
  {
    title: "Git",
    items: [
      { keys: ["j", "k"], label: "Next / previous commit (or file, in the file list)" },
      { keys: ["Enter"], label: "Open the diff" },
      { keys: ["/"], label: "Search commits" },
      { keys: ["Space"], label: "Stage / unstage the selected file" },
      { keys: ["mod+Enter"], label: "Commit (message focused)" },
    ],
  },
  {
    title: "Sidebar tree",
    items: [
      { keys: ["ArrowUp", "ArrowDown"], label: "Move" },
      { keys: ["ArrowRight", "ArrowLeft"], label: "Expand / collapse" },
      { keys: ["Enter"], label: "Open" },
    ],
  },
];

/** Generated from the action registry, so it can't drift from the bindings. */
export function ShortcutsReference() {
  const groups = (["app", "project", "service", "task"] as ActionScope[]).map((scope) => {
    const seen = new Map<string, string[]>();
    for (const a of ACTIONS) {
      if (a.scope !== scope || !a.shortcut) continue;
      const list = seen.get(a.shortcut) ?? [];
      if (!list.includes(a.label)) list.push(a.label);
      seen.set(a.shortcut, list);
    }
    return { title: SCOPE_TITLES[scope], items: [...seen].map(([k, labels]) => ({ keys: [k], label: labels.join(" / ") })) };
  });
  return (
    <div class="grid gap-x-8 gap-y-5 sm:grid-cols-2">
      <For each={[...groups, ...EXTRA]}>
        {(group) => (
          <section>
            <h3 class="mb-2 text-2xs font-semibold tracking-wide text-muted-foreground uppercase">{group.title}</h3>
            <dl class="flex flex-col gap-1.5">
              <For each={group.items}>
                {(item) => (
                  <div class="flex items-center justify-between gap-3 text-ui">
                    <dt>{item.label}</dt>
                    <dd class="flex gap-1">
                      <For each={item.keys}>{(k) => <Shortcut keys={k} />}</For>
                    </dd>
                  </div>
                )}
              </For>
            </dl>
          </section>
        )}
      </For>
    </div>
  );
}

export function ShortcutsDialog() {
  return (
    <Dialog open={shortcutsOpen()} onOpenChange={setShortcutsOpen}>
      <DialogContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>Single-key shortcuts are ignored while typing in inputs or terminals.</DialogDescription>
        </DialogHeader>
        <ShortcutsReference />
      </DialogContent>
    </Dialog>
  );
}
