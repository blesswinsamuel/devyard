import { createSignal } from "solid-js";
import { startProjectByPath } from "~/stores/data";
import { setShowAddProject, showAddProject } from "~/stores/app";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "~/components/ui/dialog";

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
        </DialogHeader>
        <form onSubmit={handleSubmit} class="space-y-4">
          <div class="space-y-1.5">
            <label for="add-project-config" class="text-xs font-medium text-muted-foreground">
              Config path
            </label>
            <input
              id="add-project-config"
              type="text"
              required
              value={configPath()}
              onInput={(e) => setConfigPath(e.currentTarget.value)}
              placeholder="/path/to/local-compose.yml"
              autofocus
              class="h-8 w-full rounded-md border border-input bg-background px-3 font-mono text-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          </div>
          <div class="space-y-1.5">
            <label for="add-project-env" class="text-xs font-medium text-muted-foreground">
              Env file <span class="opacity-60">(optional)</span>
            </label>
            <input
              id="add-project-env"
              type="text"
              value={envFile()}
              onInput={(e) => setEnvFile(e.currentTarget.value)}
              placeholder="/path/to/.env"
              class="h-8 w-full rounded-md border border-input bg-background px-3 font-mono text-xs placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
