import { createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { useNavigate } from "@solidjs/router";
import { toast } from "solid-sonner";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import { CheckboxField } from "~/components/checkbox-field";
import { api } from "~/data/client";
import { errorInfo } from "~/data/errors";
import { useProjectPathSuggestions } from "~/data/queries";
import { paths } from "~/lib/paths";
import { cn } from "~/lib/utils";
import { addProjectOpen, setAddProjectOpen } from "~/app/ui-state";

interface Suggestion {
  path: string;
  hasConfig: boolean;
  isGit: boolean;
  listed: boolean;
}

function SuggestionList(props: { label: string; items: Suggestion[]; onPick: (s: Suggestion) => void }) {
  return (
    <div role="group" aria-label={props.label} class="flex max-h-44 flex-col overflow-y-auto rounded-md border">
      <For each={props.items}>
        {(s) => (
          <button
            type="button"
            disabled={s.listed}
            class={cn(
              "focus-ring flex items-center gap-2 border-b px-2 py-1 text-left font-mono text-xs last:border-b-0",
              s.listed ? "cursor-not-allowed text-muted-foreground" : "hover:bg-accent",
            )}
            onClick={() => props.onPick(s)}
          >
            <span class="min-w-0 flex-1 truncate" title={s.path}>
              {s.path}
            </span>
            <Show when={s.listed}>
              <span class="shrink-0 text-2xs">already added</span>
            </Show>
            <Show when={s.hasConfig}>
              <span class="shrink-0 rounded-sm bg-muted px-1 text-2xs">devyard.yml</span>
            </Show>
            <Show when={s.isGit}>
              <span class="shrink-0 rounded-sm bg-muted px-1 text-2xs">git</span>
            </Show>
          </button>
        )}
      </For>
    </div>
  );
}

export function AddProjectDialog() {
  const navigate = useNavigate();
  const [path, setPath] = createSignal("");
  const [prefix, setPrefix] = createSignal("");
  const [start, setStart] = createSignal(true);
  const [build, setBuild] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [picked, setPicked] = createSignal<Suggestion>();

  // Ask for completions once typing pauses.
  let timer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(timer));
  const typed = (value: string) => {
    setPath(value);
    clearTimeout(timer);
    timer = setTimeout(() => setPrefix(value.trim()), 150);
  };
  const pick = (s: Suggestion) => {
    clearTimeout(timer);
    setPicked(s);
    // Descend into the directory: its subdirectories are suggested next.
    setPath(`${s.path}/`);
    setPrefix(`${s.path}/`);
  };

  const suggestions = useProjectPathSuggestions(prefix, addProjectOpen);
  const recent = () => (path().trim() ? [] : (suggestions.data?.recent ?? []));
  const completions = () => suggestions.data?.completions ?? [];
  // What we know about the typed directory, to explain what adding it does.
  const known = createMemo(() => {
    const dir = path().trim().replace(/\/+$/, "");
    if (picked()?.path === dir) return picked();
    return [...(suggestions.data?.completions ?? []), ...(suggestions.data?.recent ?? [])].find((s) => s.path === dir);
  });

  const reset = () => {
    clearTimeout(timer);
    setPath("");
    setPrefix("");
    setPicked(undefined);
    setError("");
    setBusy(false);
  };

  const submit = async (e: Event) => {
    e.preventDefault();
    if (!path().trim() || busy()) return;
    setBusy(true);
    setError("");
    try {
      const noConfig = known()?.hasConfig === false;
      const res = await api.addProject({
        path: path().trim(),
        start: start() && !noConfig,
        build: start() && build() && !noConfig,
      });
      const id = res.project?.id;
      toast.success(`Added ${id ?? "project"}`);
      setAddProjectOpen(false);
      reset();
      if (id) navigate(paths.project(id));
    } catch (err) {
      const info = errorInfo(err);
      setError(info.message ? `${info.reason}: ${info.message}` : info.reason);
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={addProjectOpen()}
      onOpenChange={(open) => {
        setAddProjectOpen(open);
        if (!open) reset();
      }}
    >
      <DialogContent class="sm:max-w-lg">
        <form class="flex flex-col gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Add project</DialogTitle>
            <DialogDescription>
              Add a directory to your project list. It doesn't need a <code class="font-mono">devyard.yml</code>: without
              one you get the git view and terminals. Services started from here use the daemon's environment; run{" "}
              <code class="font-mono">devyard start</code> in a shell to capture that shell's environment instead.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel for="project-path">Directory</FieldLabel>
              <Input
                id="project-path"
                class="font-mono"
                required
                autofocus
                autocomplete="off"
                spellcheck={false}
                placeholder="~/dev/my-app"
                value={path()}
                onInput={(e) => typed(e.currentTarget.value)}
              />
              <FieldDescription>An absolute path or one starting with ~.</FieldDescription>
            </Field>
            <Show when={recent().length}>
              <div class="flex flex-col gap-1">
                <span class="text-ui text-muted-foreground">Recently removed</span>
                <SuggestionList label="Recently removed projects" items={recent()} onPick={pick} />
              </div>
            </Show>
            <Show when={path().trim() && completions().length}>
              <SuggestionList label="Matching directories" items={completions()} onPick={pick} />
            </Show>
            <Show
              when={known()?.hasConfig !== false}
              fallback={
                <p class="text-ui text-muted-foreground">
                  No <code class="font-mono">devyard.yml</code> here: this adds a project with the git view and
                  terminals only. Create the file later and it is picked up.
                </p>
              }
            >
              <div class="flex flex-wrap gap-5">
                <CheckboxField label="Start now" checked={start()} onChange={setStart} />
                <CheckboxField label="Build first" checked={build()} onChange={setBuild} disabled={!start()} />
              </div>
            </Show>
            <Show when={error()}>
              <FieldError>{error()}</FieldError>
            </Show>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setAddProjectOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!path().trim() || busy()}>
              <Show when={busy()}>
                <Spinner />
              </Show>
              Add project
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
