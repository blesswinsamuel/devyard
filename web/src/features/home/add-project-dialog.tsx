import { createSignal, Show } from "solid-js";
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
import { paths } from "~/lib/paths";
import { addProjectOpen, setAddProjectOpen } from "~/app/ui-state";

export function AddProjectDialog() {
  const navigate = useNavigate();
  const [configPath, setConfigPath] = createSignal("");
  const [envFile, setEnvFile] = createSignal("");
  const [start, setStart] = createSignal(true);
  const [build, setBuild] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");

  const reset = () => {
    setConfigPath("");
    setEnvFile("");
    setError("");
    setBusy(false);
  };

  const submit = async (e: Event) => {
    e.preventDefault();
    if (!configPath().trim() || busy()) return;
    setBusy(true);
    setError("");
    try {
      const res = await api.addProject({
        configPath: configPath().trim(),
        envFile: envFile().trim(),
        start: start(),
        build: start() && build(),
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
              Register a <code class="font-mono">devyard.yml</code> with the daemon. Services started from here use the
              daemon's environment; run <code class="font-mono">devyard start</code> in a shell to capture that shell's
              environment instead.
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel for="config-path">Config path</FieldLabel>
              <Input
                id="config-path"
                class="font-mono"
                required
                autofocus
                placeholder="/Users/me/src/app/devyard.yml"
                value={configPath()}
                onInput={(e) => setConfigPath(e.currentTarget.value)}
              />
              <FieldDescription>Absolute path to the project's devyard.yml (or its directory).</FieldDescription>
            </Field>
            <Field>
              <FieldLabel for="env-file">Env file (optional)</FieldLabel>
              <Input
                id="env-file"
                class="font-mono"
                placeholder=".env next to the config"
                value={envFile()}
                onInput={(e) => setEnvFile(e.currentTarget.value)}
              />
            </Field>
            <div class="flex flex-wrap gap-5">
              <CheckboxField label="Start now" checked={start()} onChange={setStart} />
              <CheckboxField label="Build first" checked={build()} onChange={setBuild} disabled={!start()} />
            </div>
            <Show when={error()}>
              <FieldError>{error()}</FieldError>
            </Show>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setAddProjectOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!configPath().trim() || busy()}>
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
