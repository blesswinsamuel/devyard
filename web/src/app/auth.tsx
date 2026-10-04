import { Code } from "@connectrpc/connect";
import { createSignal, onMount, Show, type ParentProps } from "solid-js";
import { Logo } from "~/components/logo";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "~/components/ui/card";
import { Field, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Spinner } from "~/components/ui/spinner";
import { api } from "~/data/client";
import { errorInfo } from "~/data/errors";

/** Whether the daemon rejected the call because the dashboard needs a login. */
export function isUnauthenticated(err: unknown): boolean {
  return errorInfo(err).code === Code.Unauthenticated;
}

/**
 * Delays the app until it is known whether the dashboard wants a login:
 * unauthenticated renders the login screen instead, so no API calls are
 * made (or streams opened) before logging in.
 */
export function AuthGate(props: ParentProps) {
  const [state, setState] = createSignal<"checking" | "login" | "ok">("checking");

  onMount(async () => {
    try {
      await api.getDaemon({});
      setState("ok");
    } catch (err) {
      // Anything else (daemon unreachable, ...): render the app, which
      // shows its own reconnect banner.
      setState(isUnauthenticated(err) ? "login" : "ok");
    }
  });

  return (
    <Show when={state() !== "checking"} fallback={<CenteredSpinner />}>
      <Show when={state() === "ok"} fallback={<LoginScreen />}>
        {props.children}
      </Show>
    </Show>
  );
}

function CenteredSpinner() {
  return (
    <div class="grid h-full place-items-center" aria-label="Loading devyard">
      <Spinner class="size-6" />
    </div>
  );
}

/** The dashboard login form: POST /auth/login, then reload into the app. */
export function LoginScreen() {
  const [password, setPassword] = createSignal("");
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy()) return;
    setBusy(true);
    setError("");
    try {
      const resp = await fetch("/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: password() }),
      });
      if (resp.ok) {
        window.location.reload();
        return;
      }
      setError(resp.status === 401 ? "Wrong password." : `Login failed (HTTP ${resp.status}).`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="grid h-full place-items-center p-4">
      <Card class="w-full max-w-sm gap-4 p-6">
        <CardHeader class="items-center gap-2 text-center">
          <Logo class="size-8" />
          <CardTitle class="text-lg font-medium">devyard</CardTitle>
          <CardDescription class="text-ui">This dashboard requires a password.</CardDescription>
        </CardHeader>
        <CardContent>
          <form class="flex flex-col gap-4" onSubmit={submit}>
            <Field>
              <FieldLabel for="login-password">Password</FieldLabel>
              <Input
                id="login-password"
                type="password"
                name="password"
                autocomplete="current-password"
                autofocus
                value={password()}
                onInput={(e) => setPassword(e.currentTarget.value)}
              />
            </Field>
            <Show when={error()}>
              <p class="text-ui text-destructive text-center" role="alert">
                {error()}
              </p>
            </Show>
            <Button type="submit" class="w-full" disabled={busy()}>
              <Show when={busy()}>
                <Spinner />
              </Show>
              Log in
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
