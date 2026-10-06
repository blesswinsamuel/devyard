import { createEffect, createSignal, Show } from "solid-js";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Field, FieldDescription, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { CheckboxField } from "~/components/checkbox-field";
import { branchRequest, setBranchRequest } from "~/app/ui-state";
import { branchPending, createBranch } from "./git-data";

/** "New branch…": a name, an optional start point and whether to switch to it. */
export function BranchDialog() {
  const [name, setName] = createSignal("");
  const [checkout, setCheckout] = createSignal(true);
  createEffect(() => {
    if (branchRequest()) {
      setName("");
      setCheckout(true);
    }
  });
  const close = (created: boolean) => {
    const req = branchRequest();
    setBranchRequest(null);
    req?.resolve(created);
  };
  const submit = async (e?: Event) => {
    e?.preventDefault();
    const req = branchRequest();
    const n = name().trim();
    if (!req || !n) return;
    if (await createBranch(req.project, n, req.start, checkout())) close(true);
  };
  return (
    <Dialog open={!!branchRequest()} onOpenChange={(open) => !open && close(false)}>
      <Show when={branchRequest()}>
        {(req) => (
          <DialogContent class="sm:max-w-md">
            <form class="flex flex-col gap-4" onSubmit={submit}>
              <DialogHeader>
                <DialogTitle>New branch</DialogTitle>
                <DialogDescription>
                  <Show when={req().start} fallback="Starts at HEAD.">
                    Starts at <span class="font-mono">{req().start}</span>.
                  </Show>
                </DialogDescription>
              </DialogHeader>
              <Field>
                <FieldLabel for="branch-name">Branch name</FieldLabel>
                <Input
                  id="branch-name"
                  class="font-mono"
                  autofocus
                  placeholder="feature/my-change"
                  value={name()}
                  onInput={(e) => setName(e.currentTarget.value)}
                />
                <FieldDescription>Letters, digits and / . - _ ; git rejects anything else.</FieldDescription>
              </Field>
              <CheckboxField label="Check out the new branch" checked={checkout()} onChange={setCheckout} />
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => close(false)}>
                  Cancel
                </Button>
                <Button type="submit" disabled={!name().trim() || branchPending(req().project)}>
                  Create branch
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        )}
      </Show>
    </Dialog>
  );
}
