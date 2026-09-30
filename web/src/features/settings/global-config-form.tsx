import { createEffect, createSignal, Show } from "solid-js";
import { createStore, reconcile, unwrap } from "solid-js/store";
import { toast } from "solid-sonner";
import { Button } from "~/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Skeleton } from "~/components/ui/skeleton";
import { Spinner } from "~/components/ui/spinner";
import { Switch } from "~/components/switch";
import { Textarea } from "~/components/ui/textarea";
import { api } from "~/data/client";
import { errorInfo } from "~/data/errors";
import { queryClient, queryKeys, useGlobalConfig } from "~/data/queries";
import type { GetGlobalConfigResponse } from "~/gen/devyard/v1/control_pb";
import { getAction } from "~/data/actions";
import { runAction } from "~/app/runtime";

interface FormState {
  webHost: string;
  webPort: string;
  allowedHosts: string;
  proxyHost: string;
  proxyPort: string;
  domainSuffix: string;
  tlsEnabled: boolean;
  tlsPort: string;
  certFile: string;
  keyFile: string;
  httpRedirect: boolean;
}

function toForm(res: GetGlobalConfigResponse | undefined): FormState {
  const c = res?.config;
  return {
    webHost: c?.web?.host ?? "",
    webPort: c?.web?.port ? String(c.web.port) : "",
    allowedHosts: (c?.web?.allowedHosts ?? []).join("\n"),
    proxyHost: c?.proxy?.host ?? "",
    proxyPort: c?.proxy?.port ? String(c.proxy.port) : "",
    domainSuffix: c?.proxy?.domainSuffix ?? "",
    tlsEnabled: c?.proxy?.tls?.enabled ?? false,
    tlsPort: c?.proxy?.tls?.port ? String(c.proxy.tls.port) : "",
    certFile: c?.proxy?.tls?.certFile ?? "",
    keyFile: c?.proxy?.tls?.keyFile ?? "",
    httpRedirect: c?.proxy?.tls?.httpRedirect ?? false,
  };
}

const port = (v: string) => {
  const n = Number(v);
  return v.trim() === "" ? 0 : Number.isInteger(n) && n >= 0 && n <= 65535 ? n : NaN;
};

/** Edits $XDG_CONFIG_HOME/devyard/config.yml via Get/UpdateGlobalConfig. */
export function GlobalConfigForm() {
  const query = useGlobalConfig();
  const [form, setForm] = createStore<FormState>(toForm(undefined));
  const [dirty, setDirty] = createSignal(false);
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal("");

  createEffect(() => {
    if (query.data && !dirty()) setForm(reconcile(toForm(query.data)));
  });

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm(key, value);
    setDirty(true);
  };

  const invalid = () =>
    [form.webPort, form.proxyPort, form.tlsPort].some((p) => Number.isNaN(port(p))) ? "Ports must be 0–65535." : "";

  const save = async (e: Event) => {
    e.preventDefault();
    if (invalid() || saving()) return;
    setSaving(true);
    setError("");
    const f = unwrap(form);
    try {
      await api.updateGlobalConfig({
        config: {
          web: {
            host: f.webHost.trim(),
            port: port(f.webPort),
            allowedHosts: f.allowedHosts
              .split(/[\n,]/)
              .map((h) => h.trim())
              .filter(Boolean),
          },
          proxy: {
            host: f.proxyHost.trim(),
            port: port(f.proxyPort),
            domainSuffix: f.domainSuffix.trim(),
            tls: {
              enabled: f.tlsEnabled,
              port: port(f.tlsPort),
              certFile: f.certFile.trim(),
              keyFile: f.keyFile.trim(),
              httpRedirect: f.httpRedirect,
            },
          },
        },
      });
      setDirty(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.globalConfig });
      toast.success("Settings saved", {
        description: "Listener changes apply after a daemon restart.",
        action: { label: "Restart daemon", onClick: () => void runAction(getAction("daemon.restart"), { kind: "app" }) },
      });
    } catch (err) {
      const info = errorInfo(err);
      setError(info.message ? `${info.reason}: ${info.message}` : info.reason);
    } finally {
      setSaving(false);
    }
  };

  const text = (key: keyof FormState, label: string, opts: { placeholder?: string; mono?: boolean; description?: string } = {}) => (
    <Field>
      <FieldLabel for={`gc-${key}`}>{label}</FieldLabel>
      <Input
        id={`gc-${key}`}
        class={opts.mono ? "font-mono" : undefined}
        placeholder={opts.placeholder}
        value={form[key] as string}
        onInput={(e) => set(key, e.currentTarget.value as never)}
      />
      <Show when={opts.description}>
        <FieldDescription>{opts.description}</FieldDescription>
      </Show>
    </Field>
  );

  const toggle = (key: "tlsEnabled" | "httpRedirect", label: string, description: string) => (
    <Field orientation="horizontal">
      <Switch id={`gc-${key}`} checked={form[key]} onChange={(v: boolean) => set(key, v)} />
      <div class="flex flex-col gap-0.5">
        <FieldLabel for={`gc-${key}`}>{label}</FieldLabel>
        <FieldDescription>{description}</FieldDescription>
      </div>
    </Field>
  );

  return (
    <Show
      when={!query.isPending}
      fallback={
        <div class="flex flex-col gap-2" aria-hidden="true">
          <Skeleton class="h-8 w-full" />
          <Skeleton class="h-8 w-full" />
        </div>
      }
    >
      <form class="flex flex-col gap-6" onSubmit={save}>
        <Show when={query.data?.path}>
          <p class="text-ui text-muted-foreground">
            Stored in <code class="font-mono text-2xs">{query.data!.path}</code>
          </p>
        </Show>
        <Show when={query.isError}>
          <p class="text-ui text-destructive">Couldn't load settings: {errorInfo(query.error).message}</p>
        </Show>
        <FieldSet>
          <FieldLegend>Dashboard</FieldLegend>
          <FieldGroup class="grid gap-4 sm:grid-cols-2">
            {text("webHost", "Host", { placeholder: "127.0.0.1", mono: true })}
            {text("webPort", "Port", { placeholder: "9090", mono: true, description: "0 picks a free port." })}
          </FieldGroup>
          <Field>
            <FieldLabel for="gc-allowedHosts">Allowed hosts</FieldLabel>
            <Textarea
              id="gc-allowedHosts"
              class="min-h-20 font-mono"
              placeholder={"devyard.example.dev\n.example.dev"}
              value={form.allowedHosts}
              onInput={(e) => set("allowedHosts", e.currentTarget.value)}
            />
            <FieldDescription>
              Extra Host headers the dashboard accepts, one per line (a leading dot matches subdomains). Loopback and the
              proxy domain are always allowed; other hosts get 403 to block DNS rebinding.
            </FieldDescription>
          </Field>
        </FieldSet>
        <FieldSet>
          <FieldLegend>Reverse proxy</FieldLegend>
          <FieldGroup class="grid gap-4 sm:grid-cols-3">
            {text("proxyHost", "Host", { placeholder: "127.0.0.1", mono: true })}
            {text("proxyPort", "Port", { placeholder: "80", mono: true })}
            {text("domainSuffix", "Domain suffix", { placeholder: "localhost", mono: true })}
          </FieldGroup>
          {toggle("tlsEnabled", "HTTPS", "Serve proxied services over TLS.")}
          <Show when={form.tlsEnabled}>
            <FieldGroup class="grid gap-4 sm:grid-cols-3">
              {text("tlsPort", "TLS port", { placeholder: "443", mono: true })}
              {text("certFile", "Certificate file", { mono: true })}
              {text("keyFile", "Key file", { mono: true })}
            </FieldGroup>
            {toggle("httpRedirect", "Redirect HTTP to HTTPS", "Plain-HTTP requests get a redirect.")}
          </Show>
        </FieldSet>
        <Show when={invalid() || error()}>
          <p class="text-ui text-destructive" role="alert">
            {invalid() || error()}
          </p>
        </Show>
        <div class="flex gap-2">
          <Button type="submit" disabled={!dirty() || saving() || !!invalid()}>
            <Show when={saving()}>
              <Spinner />
            </Show>
            Save
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={!dirty()}
            onClick={() => {
              setForm(reconcile(toForm(query.data)));
              setDirty(false);
              setError("");
            }}
          >
            Discard
          </Button>
        </div>
      </form>
    </Show>
  );
}
