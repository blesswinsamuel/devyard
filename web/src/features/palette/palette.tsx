import { createMemo, createSignal, For, Show, type JSX } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { GitBranch, House, Settings } from "lucide-solid";
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "~/components/ui/command";
import { StatusDot } from "~/components/status";
import { actionsFor, actionTitle, type Action, type ActionTarget } from "~/data/actions";
import { projectList, servicesOf, tasksOf } from "~/data/entities";
import { shortcutKeys } from "~/lib/keyboard";
import { paths } from "~/lib/paths";
import { projectTone, serviceTone, taskTone } from "~/lib/status";
import { contextFor, routeTarget, runAction } from "~/app/runtime";
import { paletteOpen, setPaletteOpen } from "~/app/ui-state";

interface Item {
  value: string;
  label: string;
  detail?: string;
  icon?: JSX.Element;
  shortcut?: string;
  destructive?: boolean;
  run: () => void;
}

function actionItem(action: Action, target: ActionTarget, withDetail: boolean): Item {
  const ctx = contextFor(target);
  const label = actionTitle(action, ctx);
  const detail = withDetail && target.kind !== "app" ? target.project : undefined;
  const Icon = action.icon;
  return {
    value: `${label} ${detail ?? ""} ${action.id}`,
    label,
    detail,
    icon: Icon ? <Icon /> : undefined,
    shortcut: action.shortcut,
    destructive: action.destructive,
    run: () => void runAction(action, target),
  };
}

/** ⌘K: navigation targets plus every registry action. */
export function CommandPalette() {
  const navigate = useNavigate();
  const [search, setSearch] = createSignal("");

  const go = (path: string) => () => navigate(path);

  const current = createMemo<Item[]>(() => {
    if (!paletteOpen()) return [];
    const target = routeTarget();
    if (target.kind === "app") return [];
    return actionsFor(contextFor(target), { inherit: true }).map((a) => actionItem(a, target, false));
  });

  const currentTitle = () => {
    const t = routeTarget();
    return t.kind === "app" ? "" : t.kind === "project" ? t.project : `${t.name} · ${t.project}`;
  };

  const navigation = createMemo<Item[]>(() => {
    if (!paletteOpen()) return [];
    const items: Item[] = [
      { value: "Projects home", label: "Projects", icon: <House />, run: go(paths.home()) },
      { value: "Settings", label: "Settings", icon: <Settings />, run: go(paths.settings()) },
    ];
    for (const p of projectList()) {
      items.push({
        value: `project ${p.id}`,
        label: p.id,
        detail: "project",
        icon: <StatusDot tone={projectTone(p)} />,
        run: go(paths.project(p.id)),
      });
      for (const s of servicesOf(p.id))
        items.push({
          value: `service ${s.name} ${p.id}`,
          label: s.name,
          detail: `service · ${p.id}`,
          icon: <StatusDot tone={serviceTone(s)} />,
          run: go(paths.service(p.id, s.name)),
        });
      for (const t of tasksOf(p.id))
        items.push({
          value: `task ${t.name} ${p.id}`,
          label: t.name,
          detail: `task · ${p.id}`,
          icon: <StatusDot tone={taskTone(t)} />,
          run: go(paths.task(p.id, t.name)),
        });
      items.push({
        value: `git ${p.id}`,
        label: `Git · ${p.id}`,
        icon: <GitBranch />,
        run: go(paths.git(p.id)),
      });
    }
    return items;
  });

  const general = createMemo<Item[]>(() =>
    paletteOpen()
      ? actionsFor(contextFor({ kind: "app" }), { includeApp: true })
          .filter((a) => a.id !== "app.palette" && a.group !== "navigation")
          .map((a) => actionItem(a, { kind: "app" }, false))
      : [],
  );

  // Every entity's actions, only while searching (keeps the empty list short).
  const everything = createMemo<Item[]>(() => {
    if (!paletteOpen() || !search().trim()) return [];
    const cur = routeTarget();
    const items: Item[] = [];
    const push = (target: ActionTarget) => {
      if (JSON.stringify(target) === JSON.stringify(cur)) return;
      for (const a of actionsFor(contextFor(target))) items.push(actionItem(a, target, true));
    };
    for (const p of projectList()) {
      push({ kind: "project", project: p.id });
      for (const s of servicesOf(p.id)) push({ kind: "service", project: p.id, name: s.name });
      for (const t of tasksOf(p.id)) push({ kind: "task", project: p.id, name: t.name });
    }
    return items;
  });

  const select = (item: Item) => {
    setPaletteOpen(false);
    setSearch("");
    // Let the dialog close (and restore focus) before actions open dialogs.
    queueMicrotask(item.run);
  };

  const Group = (props: { heading: string; items: Item[] }) => (
    <Show when={props.items.length}>
      <CommandGroup heading={props.heading}>
        <For each={props.items}>
          {(item) => (
            <CommandItem value={item.value} onSelect={() => select(item)} class={item.destructive ? "text-destructive" : undefined}>
              {item.icon}
              <span class="truncate">{item.label}</span>
              <Show when={item.detail}>
                <span class="truncate text-2xs text-muted-foreground">{item.detail}</span>
              </Show>
              <Show when={item.shortcut}>
                <CommandShortcut>{shortcutKeys(item.shortcut!).join("")}</CommandShortcut>
              </Show>
            </CommandItem>
          )}
        </For>
      </CommandGroup>
    </Show>
  );

  return (
    <CommandDialog
      open={paletteOpen()}
      onOpenChange={(open) => {
        setPaletteOpen(open);
        if (!open) setSearch("");
      }}
      title="Command palette"
      description="Jump to a project, service or task, or run an action"
    >
      <Command>
        <CommandInput placeholder="Search projects, services, actions…" value={search()} onValueChange={setSearch} />
        <CommandList class="max-h-96">
          <CommandEmpty>No results.</CommandEmpty>
          <Group heading={currentTitle() ? `Actions · ${currentTitle()}` : "Actions"} items={current()} />
          <Group heading="Go to" items={navigation()} />
          <Group heading="General" items={general()} />
          <Group heading="All actions" items={everything()} />
        </CommandList>
      </Command>
    </CommandDialog>
  );
}

