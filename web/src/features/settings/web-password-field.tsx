import { createSignal, Show } from "solid-js";
import { toast } from "solid-sonner";
import { confirm } from "~/app/ui-state";
import { Button } from "~/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import { api } from "~/data/client";
import { errorDescription, errorInfo } from "~/data/errors";
import { queryClient, queryKeys, useGlobalConfig } from "~/data/queries";

/**
 * The dashboard password (web.password_hash): set or remove it via
 * SetWebPassword. Applies without a restart; the hash never crosses the
 * wire, so this only shows whether one is set.
 */
export function WebPasswordField() {
  const query = useGlobalConfig();
  const [password, setPassword] = createSignal("");
  const [busy, setBusy] = createSignal<"" | "set" | "clear">("");

  const enabled = () => query.data?.config?.web?.passwordSet ?? false;

  const apply = async (req: { password?: string; clear?: boolean }) => {
    setBusy(req.clear ? "clear" : "set");
    try {
      await api.setWebPassword(req);
      if (req.password) {
        // Setting a password invalidates every session (fresh key). Log
        // this device back in so the open app keeps working.
        await fetch("/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ password: req.password }),
        });
      }
      setPassword("");
      await queryClient.invalidateQueries({ queryKey: queryKeys.globalConfig });
      if (req.clear) toast.success("Password removed", { description: "The dashboard is open to anyone who can reach it." });
      else toast.success("Password set", { description: "Applies immediately; other devices must log in again." });
    } catch (err) {
      toast.error("Couldn't save the password", { description: errorDescription(err) });
    } finally {
      setBusy("");
    }
  };

  const remove = async () => {
    if (busy()) return;
    if (
      !(await confirm({
        title: "Remove the dashboard password?",
        description: "Anyone who can reach the dashboard can then use it, including its terminals.",
        confirmLabel: "Remove password",
      }))
    )
      return;
    await apply({ clear: true });
  };

  const set = async (e: Event) => {
    e.preventDefault();
    if (busy() || !password()) return;
    await apply({ password: password() });
  };

  return (
    <form class="flex flex-col gap-4" onSubmit={set}>
      <Field>
        <FieldLabel for="wp-password">Password</FieldLabel>
        <div class="flex gap-2">
          <Input
            id="wp-password"
            type="password"
            autocomplete="new-password"
            placeholder={enabled() ? "New password" : "Set a password…"}
            value={password()}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
          <Button type="submit" disabled={busy() !== "" || !password()}>
            <Show when={busy() === "set"}>
              <Spinner />
            </Show>
            Set
          </Button>
          <Show when={enabled()}>
            <Button type="button" variant="destructive" disabled={busy() !== ""} onClick={() => void remove()}>
              <Show when={busy() === "clear"}>
                <Spinner />
              </Show>
              Remove
            </Button>
          </Show>
        </div>
        <FieldDescription>
          {enabled()
            ? "A password is set: the dashboard requires a login. A new one applies immediately and logs every device out."
            : "No password set. With one, the dashboard asks for a login before anything loads."}
        </FieldDescription>
      </Field>
      <Show when={query.isError}>
        <p class="text-ui text-destructive">Couldn't load settings: {errorInfo(query.error).message}</p>
      </Show>
    </form>
  );
}
