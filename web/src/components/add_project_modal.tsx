import { createSignal } from "solid-js";
import { startProjectByPath } from "~/stores/data";
import { setShowAddProject, showAddProject } from "~/stores/app";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";

export function AddProjectModal() {
  const [configPath, setConfigPath] = createSignal("");
  const [envFile, setEnvFile] = createSignal("");

  const handleSubmit = (e: SubmitEvent) => {
    e.preventDefault();
    const path = configPath().trim();
    if (!path) return;
    startProjectByPath(path, envFile().trim() || undefined);
    reset();
  };

  const reset = () => {
    setConfigPath("");
    setEnvFile("");
    setShowAddProject(false);
  };

  return (
    <Dialog open={showAddProject()} onOpenChange={setShowAddProject}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Project</DialogTitle>
          <DialogDescription class="sr-only">Register a new local-compose project from a config path.</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} class="space-y-4">
          <div class="space-y-1.5">
            <Label for="add-project-config" class="text-xs text-muted-foreground">
              Config path
            </Label>
            <Input
              id="add-project-config"
              type="text"
              required
              value={configPath()}
              onInput={(e) => setConfigPath(e.currentTarget.value)}
              placeholder="/path/to/local-compose.yml"
              autofocus
              class="rounded-md px-3 font-mono text-xs md:text-xs"
            />
          </div>
          <div class="space-y-1.5">
            <Label for="add-project-env" class="text-xs text-muted-foreground">
              Env file <span class="opacity-60">(optional)</span>
            </Label>
            <Input
              id="add-project-env"
              type="text"
              value={envFile()}
              onInput={(e) => setEnvFile(e.currentTarget.value)}
              placeholder="/path/to/.env"
              class="rounded-md px-3 font-mono text-xs md:text-xs"
            />
          </div>
          <div class="flex justify-end gap-2 pt-1">
            <Button type="button" variant="outline" size="sm" onClick={reset}>
              Cancel
            </Button>
            <Button type="submit" size="sm">
              Add & start
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
