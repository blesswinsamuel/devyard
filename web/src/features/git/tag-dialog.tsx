import { createEffect, createSignal, Show } from "solid-js";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Field, FieldDescription, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Textarea } from "~/components/ui/textarea";
import { setTagRequest, tagRequest } from "~/app/ui-state";
import { createTag, tagPending } from "./git-data";

/** "New tag…": a name and an optional annotated message, at HEAD or a commit. */
export function TagDialog() {
  const [name, setName] = createSignal("");
  const [message, setMessage] = createSignal("");
  createEffect(() => {
    if (tagRequest()) {
      setName("");
      setMessage("");
    }
  });
  const close = (created: boolean) => {
    const req = tagRequest();
    setTagRequest(null);
    req?.resolve(created);
  };
  const submit = async (e?: Event) => {
    e?.preventDefault();
    const req = tagRequest();
    const n = name().trim();
    if (!req || !n) return;
    if (await createTag(req.project, n, req.target, message().trim())) close(true);
  };
  return (
    <Dialog open={!!tagRequest()} onOpenChange={(open) => !open && close(false)}>
      <Show when={tagRequest()}>
        {(req) => (
          <DialogContent class="sm:max-w-md">
            <form class="flex flex-col gap-4" onSubmit={submit}>
              <DialogHeader>
                <DialogTitle>New tag</DialogTitle>
                <DialogDescription>
                  <Show when={req().target} fallback="Tags HEAD.">
                    Tags <span class="font-mono">{req().target}</span>.
                  </Show>
                </DialogDescription>
              </DialogHeader>
              <Field>
                <FieldLabel for="tag-name">Tag name</FieldLabel>
                <Input
                  id="tag-name"
                  class="font-mono"
                  autofocus
                  placeholder="v1.0.0"
                  value={name()}
                  onInput={(e) => setName(e.currentTarget.value)}
                />
                <FieldDescription>Often a version like v1.2.3.</FieldDescription>
              </Field>
              <Field>
                <FieldLabel for="tag-message">Message (optional)</FieldLabel>
                <Textarea
                  id="tag-message"
                  rows={2}
                  class="min-h-14 resize-y font-mono text-xs"
                  placeholder="An annotated tag message"
                  value={message()}
                  onInput={(e) => setMessage(e.currentTarget.value)}
                />
                <FieldDescription>Leave empty for a lightweight tag.</FieldDescription>
              </Field>
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => close(false)}>
                  Cancel
                </Button>
                <Button type="submit" disabled={!name().trim() || tagPending(req().project)}>
                  Create tag
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        )}
      </Show>
    </Dialog>
  );
}
