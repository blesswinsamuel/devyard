import { createEffect, createMemo, createSignal, Show } from "solid-js";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { joinArgs, splitArgs } from "~/lib/args";
import { argsRequest, setArgsRequest } from "~/app/ui-state";

/** "Run with args…": shell-style argument entry, pre-filled with the last run's args. */
export function RunArgsDialog() {
  const [text, setText] = createSignal("");
  createEffect(() => {
    const req = argsRequest();
    if (req) setText(joinArgs(req.task.args));
  });
  const parsed = createMemo(() => splitArgs(text()));
  const close = (args: string[] | null) => {
    const req = argsRequest();
    setArgsRequest(null);
    req?.resolve(args);
  };
  return (
    <Dialog open={!!argsRequest()} onOpenChange={(open) => !open && close(null)}>
      <Show when={argsRequest()}>
        {(req) => (
          <DialogContent class="sm:max-w-lg">
            <form
              class="flex flex-col gap-4"
              onSubmit={(e) => {
                e.preventDefault();
                const p = parsed();
                if ("args" in p) close(p.args);
              }}
            >
              <DialogHeader>
                <DialogTitle>Run {req().task.name} with arguments</DialogTitle>
                <DialogDescription>Arguments are appended to the task's command. Quote values containing spaces.</DialogDescription>
              </DialogHeader>
              <Field>
                <FieldLabel for="task-args">Arguments</FieldLabel>
                <Input
                  id="task-args"
                  class="font-mono"
                  autofocus
                  placeholder="--verbose 'some value'"
                  value={text()}
                  onInput={(e) => setText(e.currentTarget.value)}
                  aria-invalid={"error" in parsed() || undefined}
                />
                <Show
                  when={"error" in parsed() && (parsed() as { error: string }).error}
                  fallback={
                    <FieldDescription class="font-mono break-all">
                      $ {req().task.spec.command} {"args" in parsed() ? joinArgs((parsed() as { args: string[] }).args) : ""}
                    </FieldDescription>
                  }
                >
                  {(err) => <FieldError>{err()}</FieldError>}
                </Show>
              </Field>
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => close(null)}>
                  Cancel
                </Button>
                <Button type="submit" disabled={"error" in parsed()}>
                  Run
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        )}
      </Show>
    </Dialog>
  );
}
