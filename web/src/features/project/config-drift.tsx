import { createSignal, For, Show } from "solid-js";
import { FileDiff, TriangleAlert } from "lucide-solid";
import { ActionButton } from "~/components/actions";
import { Alert, AlertDescription, AlertTitle } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import type { ConfigChangeEntity, ProjectEntity } from "~/data/entities";
import { cn } from "~/lib/utils";

const MARK: Record<string, string> = { added: "+", removed: "−", changed: "~" };
const MARK_CLASS: Record<string, string> = { added: "text-success", removed: "text-destructive", changed: "text-warning" };

function ChangeRow(props: { change: ConfigChangeEntity }) {
  const c = () => props.change;
  return (
    <li class="flex items-start gap-2">
      <span class={cn("w-3 shrink-0 text-center font-mono", MARK_CLASS[c().op])} aria-label={c().op}>
        {MARK[c().op] ?? "~"}
      </span>
      <span class="min-w-0">
        <span class="text-muted-foreground">{c().kind} </span>
        <span class="font-medium">{c().name}</span>
        <Show when={c().restart}>
          <span class="ml-1.5 rounded-sm bg-warning/15 px-1 text-2xs text-warning">restarts</span>
        </Show>
        <Show when={c().details.length}>
          <span class="block font-mono text-2xs break-words text-muted-foreground">{c().details.join(" · ")}</span>
        </Show>
      </span>
    </li>
  );
}

const lineClass = (line: string) => {
  if (line.startsWith("+++") || line.startsWith("---")) return "font-semibold text-foreground";
  if (line.startsWith("@@")) return "text-info";
  if (line.startsWith("+")) return "bg-success/10 text-success";
  if (line.startsWith("-")) return "bg-destructive/10 text-destructive";
  return "text-muted-foreground";
};

/** A unified diff with added and removed lines coloured. */
export function DiffView(props: { diff: string }) {
  return (
    <pre class="max-h-[60vh] overflow-auto rounded-md border bg-muted/30 py-2 font-mono text-2xs leading-relaxed" aria-label="Config diff">
      <For each={props.diff.split("\n")}>
        {(line) => <div class={cn("px-3 whitespace-pre-wrap break-all", lineClass(line))}>{line || " "}</div>}
      </For>
    </pre>
  );
}

/**
 * Shown on the project page when the config files on disk no longer match
 * what the project runs: valid changes wait for Apply (the reload policy),
 * files that do not load are reported while the old config keeps running.
 */
export function ConfigDriftBanner(props: { project: ProjectEntity }) {
  const [diffOpen, setDiffOpen] = createSignal(false);
  const drift = () => props.project.drift;
  const target = () => ({ kind: "project" as const, project: props.project.id });
  return (
    <Show when={drift()}>
      {(d) => (
        <>
          <Show
            when={d().state === "pending"}
            fallback={
              <Alert variant="destructive">
                <TriangleAlert />
                <AlertTitle>The config files changed but don't load</AlertTitle>
                <AlertDescription>
                  <p class="font-mono text-2xs break-all whitespace-pre-wrap">{d().error}</p>
                  <p class="mt-1">What runs is unchanged. Fix the file; it is checked again automatically.</p>
                  <Show when={d().diff}>
                    <Button variant="outline" size="xs" class="mt-2" onClick={() => setDiffOpen(true)}>
                      <FileDiff /> View diff
                    </Button>
                  </Show>
                </AlertDescription>
              </Alert>
            }
          >
            <Alert>
              <FileDiff />
              <AlertTitle>The config changed on disk</AlertTitle>
              <AlertDescription>
                <ul class="mt-1 flex flex-col gap-1">
                  <For each={d().changes.slice(0, 8)}>{(c) => <ChangeRow change={c} />}</For>
                </ul>
                <Show when={d().changes.length > 8}>
                  <p class="mt-1 text-muted-foreground">…and {d().changes.length - 8} more.</p>
                </Show>
                <div class="mt-3 flex flex-wrap items-center gap-2">
                  <ActionButton id="project.reload" target={target()} label="Apply changes" />
                  <Button variant="outline" size="sm" onClick={() => setDiffOpen(true)}>
                    <FileDiff /> View diff
                  </Button>
                  <span class="text-2xs text-muted-foreground">
                    Nothing changes until you apply; services that did not change keep running.
                  </span>
                </div>
              </AlertDescription>
            </Alert>
          </Show>
          <Dialog open={diffOpen()} onOpenChange={setDiffOpen}>
            <DialogContent class="sm:max-w-3xl">
              <DialogHeader>
                <DialogTitle>Config changes of {props.project.id}</DialogTitle>
                <DialogDescription>
                  The YAML files as they are on disk compared with what is running. Changes to env files are listed by
                  variable name only.
                </DialogDescription>
              </DialogHeader>
              <DiffView diff={d().diff || "(no change to the YAML files)"} />
            </DialogContent>
          </Dialog>
        </>
      )}
    </Show>
  );
}
