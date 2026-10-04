import { For, Show, type JSX } from "solid-js";
import { Monitor, Moon, Sun } from "lucide-solid";
import { toast } from "solid-sonner";
import { Field, FieldDescription, FieldLabel } from "~/components/ui/field";
import { Switch } from "~/components/switch";
import { ToggleGroup, ToggleGroupItem } from "~/components/ui/toggle-group";
import { ActionButton } from "~/components/actions";
import { Page, PageHeader, usePageTitle } from "~/components/page";
import { notificationsEnabled, setNotificationsEnabled } from "~/data/crash";
import { entities, toDaemon, type DaemonEntity } from "~/data/entities";
import { useDaemonInfo } from "~/data/queries";
import { formatBytes, formatDuration, now } from "~/lib/format";
import { setThemePref, themePref, type ThemePref } from "~/lib/theme";
import { ShortcutsReference } from "~/features/shortcuts/shortcuts";
import { GlobalConfigForm } from "./global-config-form";
import { WebPasswordField } from "./web-password-field";

function Card(props: { title: string; description?: string; children: JSX.Element; id: string }) {
  return (
    <section class="flex flex-col gap-4 rounded-lg border bg-card p-4 md:p-5" aria-labelledby={props.id}>
      <div class="flex flex-col gap-1">
        <h2 id={props.id} class="text-sm font-semibold">
          {props.title}
        </h2>
        <Show when={props.description}>
          <p class="text-ui text-muted-foreground">{props.description}</p>
        </Show>
      </div>
      {props.children}
    </section>
  );
}

function DaemonInfoGrid() {
  // Watch keeps the basics live; GetDaemon refreshes memory/goroutines.
  const query = useDaemonInfo({ refetchIntervalMs: 5_000 });
  const info = (): DaemonEntity | null => (query.data ? toDaemon(query.data) : entities.state.daemon);
  const rows = (d: DaemonEntity): [string, string][] => [
    ["Version", `${d.version || "dev"} (${d.goVersion})`],
    ["PID", String(d.pid)],
    ["Uptime", d.startedAt ? formatDuration(now() - d.startedAt) : "—"],
    ["Memory", `${formatBytes(d.memoryRss)} RSS · ${formatBytes(d.memoryHeap)} heap`],
    ["Goroutines", String(d.goroutines)],
    ["Dashboard", d.webAddr || "disabled"],
    ["Proxy", d.proxyAddr || "disabled"],
    ["Proxy TLS", d.proxyTlsAddr || "disabled"],
    ["Domain suffix", d.domainSuffix || "—"],
  ];
  return (
    <Show when={info()} fallback={<p class="text-ui text-muted-foreground">Not connected.</p>}>
      {(d) => (
        <>
          <Show when={d().draining}>
            <p class="text-ui font-medium text-warning">The daemon is shutting down.</p>
          </Show>
          <dl class="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
            <For each={rows(d())}>
              {([label, value]) => (
                <div class="flex flex-col">
                  <dt class="text-2xs text-muted-foreground">{label}</dt>
                  <dd class="tabular truncate font-mono text-xs" title={value}>
                    {value}
                  </dd>
                </div>
              )}
            </For>
          </dl>
          <div class="flex flex-wrap gap-2">
            <ActionButton id="daemon.restart" target={{ kind: "app" }} label="Restart daemon" />
            <ActionButton id="daemon.restart-services" target={{ kind: "app" }} label="Restart daemon & services" />
            <ActionButton id="daemon.stop" target={{ kind: "app" }} />
          </div>
          <p class="text-2xs text-muted-foreground">
            A plain restart keeps services running; the new daemon re-adopts them.
          </p>
        </>
      )}
    </Show>
  );
}

const THEMES: { value: ThemePref; label: string; icon: (p: { class?: string }) => JSX.Element }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

async function toggleNotifications(on: boolean) {
  if (!on) return setNotificationsEnabled(false);
  if (typeof Notification === "undefined") {
    toast.error("This browser doesn't support notifications");
    return;
  }
  const permission = Notification.permission === "default" ? await Notification.requestPermission() : Notification.permission;
  if (permission !== "granted") {
    toast.error("Notifications are blocked", { description: "Allow them for this site in your browser settings." });
    return setNotificationsEnabled(false);
  }
  setNotificationsEnabled(true);
}

export function SettingsPage() {
  usePageTitle(() => "Settings");
  return (
    <Page class="max-w-4xl">
      <PageHeader title="Settings" />
      <Card id="appearance" title="Appearance">
        <ToggleGroup
          variant="outline"
          value={themePref()}
          onChange={(v: string | null) => v && setThemePref(v as ThemePref)}
          aria-label="Theme"
        >
          <For each={THEMES}>
            {(t) => (
              <ToggleGroupItem value={t.value} aria-label={t.label} class="gap-1.5 px-3">
                <t.icon class="size-4" />
                {t.label}
              </ToggleGroupItem>
            )}
          </For>
        </ToggleGroup>
      </Card>
      <Card id="notifications" title="Notifications">
        <Field orientation="horizontal">
          <Switch id="notify" checked={notificationsEnabled()} onChange={(v: boolean) => void toggleNotifications(v)} />
          <div class="flex flex-col gap-0.5">
            <FieldLabel for="notify">Browser notifications for crashes</FieldLabel>
            <FieldDescription>
              When the tab is in the background, notify when a service crashes, turns unhealthy, or a task fails. In-app toasts
              are always on.
            </FieldDescription>
          </div>
        </Field>
      </Card>
      <Card id="daemon" title="Daemon">
        <DaemonInfoGrid />
      </Card>
      <Card id="global-config" title="Global configuration" description="Listener and proxy settings shared by all projects.">
        <GlobalConfigForm />
      </Card>
      <Card
        id="web-password"
        title="Dashboard password"
        description="Optional. When set, the dashboard, terminals and logs require a login."
      >
        <WebPasswordField />
      </Card>
      <Card id="shortcuts" title="Keyboard shortcuts">
        <ShortcutsReference />
      </Card>
    </Page>
  );
}
