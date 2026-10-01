import { For, Show, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import { CopyButton } from "~/components/copy-button";
import { HealthIndicator, StatusDot } from "~/components/status";
import { UrlLinks } from "~/components/url-links";
import { getService, type ServiceEntity } from "~/data/entities";
import { usePorts } from "~/data/queries";
import { formatDateTime, formatDuration, now } from "~/lib/format";
import { paths } from "~/lib/paths";
import { serviceLabel, serviceTone } from "~/lib/status";

function Row(props: { label: string; children: JSX.Element }) {
  return (
    <div class="grid grid-cols-1 gap-1 border-b py-2 last:border-b-0 sm:grid-cols-4 sm:gap-4">
      <dt class="text-ui text-muted-foreground">{props.label}</dt>
      <dd class="min-w-0 text-ui sm:col-span-3">{props.children}</dd>
    </div>
  );
}

function Code(props: { text: string }) {
  return (
    <span class="inline-flex max-w-full items-start gap-1">
      <code class="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-xs break-all whitespace-pre-wrap">{props.text}</code>
      <CopyButton text={props.text} />
    </span>
  );
}

/** A dependency with its live status and whether it is ready: running and,
 * when it has a readiness probe, healthy. */
function DependencyRow(props: { project: string; dep: string }) {
  const s = () => getService(props.project, props.dep);
  const met = () => {
    const svc = s();
    if (!svc) return false;
    if (svc.status === "exited") return svc.exitCode === 0;
    return svc.status === "running" && (!svc.spec.ready || svc.health === "healthy");
  };
  return (
    <li class="flex flex-wrap items-center gap-2">
      <Show when={s()} fallback={<StatusDot tone="muted" />}>
        {(svc) => <StatusDot tone={serviceTone(svc())} />}
      </Show>
      <A href={paths.service(props.project, props.dep)} class="focus-ring rounded-sm font-medium hover:underline">
        {props.dep}
      </A>
      <span class={met() ? "text-2xs text-success" : "text-2xs text-warning"}>
        {s() ? `${serviceLabel(s()!)}${s()!.health ? `, ${s()!.health}` : ""}` : "unknown service"}
        {met() ? " ✓" : ""}
      </span>
    </li>
  );
}

export function ServiceDetails(props: { service: ServiceEntity }) {
  const s = () => props.service;
  const spec = () => s().spec;
  const ports = usePorts(() => props.service.project);
  const listening = () => (ports.data ?? []).filter((p) => p.service === s().name);
  return (
    <div class="grid gap-6 lg:grid-cols-2">
      <section class="rounded-lg border bg-card px-4 py-2" aria-label="Definition">
        <h3 class="pt-2 pb-1 text-sm font-semibold">Definition</h3>
        <dl>
          <Row label="Command">
            <Code text={spec().command} />
          </Row>
          <Show when={spec().buildCommand}>
            <Row label="Build">
              <Code text={spec().buildCommand} />
            </Row>
          </Show>
          <Row label="Directory">
            <span class="font-mono text-xs break-all">{spec().dir || "—"}</span>
          </Row>
          <Row label="Restart">{spec().restart || "on-failure"}</Row>
          <Show when={!spec().autostart}>
            <Row label="Autostart">no (starts only when named)</Row>
          </Show>
          <Show when={spec().stopSignal}>
            <Row label="Stop signal">
              <span class="font-mono text-xs">{spec().stopSignal}</span>
            </Row>
          </Show>
          <Row label="TTY">{spec().tty ? "yes (attachable)" : "no"}</Row>
          <Row label="Depends on">
            <Show when={spec().dependsOn.length} fallback={<span class="text-muted-foreground">nothing</span>}>
              <ul class="flex flex-col gap-1">
                <For each={spec().dependsOn}>{(d) => <DependencyRow project={s().project} dep={d} />}</For>
              </ul>
            </Show>
          </Row>
          <Row label="Ports">
            <Show when={spec().ports.length} fallback={<span class="text-muted-foreground">—</span>}>
              <span class="flex flex-wrap gap-2">
                <For each={spec().ports}>
                  {(p) => (
                    <span class="tabular rounded-sm bg-muted px-1.5 font-mono text-xs">
                      {p.name ? `${p.name}:` : ""}
                      {p.port}
                      {p.auto ? " (auto)" : ""}
                    </span>
                  )}
                </For>
              </span>
            </Show>
          </Row>
          <Row label="Listening">
            <Show
              when={listening().length}
              fallback={<span class="text-muted-foreground">{ports.isPending ? "…" : "no open ports"}</span>}
            >
              <span class="flex flex-wrap gap-2">
                <For each={listening()}>
                  {(p) => (
                    <span class="tabular rounded-sm bg-muted px-1.5 font-mono text-xs" title={`pid ${p.pid}`}>
                      {p.ip || "*"}:{p.port}/{p.protocol}
                    </span>
                  )}
                </For>
              </span>
            </Show>
          </Row>
          <Row label="URLs">
            <Show when={s().urls.length} fallback={<span class="text-muted-foreground">—</span>}>
              <UrlLinks urls={s().urls} max={10} />
            </Show>
          </Row>
          <Row label="Environment">
            <Show when={spec().envKeys.length} fallback={<span class="text-muted-foreground">no extra variables</span>}>
              <span class="flex flex-wrap gap-1">
                <For each={spec().envKeys}>
                  {(k) => <span class="rounded-sm bg-muted px-1.5 font-mono text-2xs">{k}</span>}
                </For>
              </span>
              <p class="mt-1 text-2xs text-muted-foreground">Values are never sent to the browser.</p>
            </Show>
          </Row>
        </dl>
      </section>

      <div class="flex flex-col gap-6">
        <section class="rounded-lg border bg-card px-4 py-2" aria-label="Runtime">
          <h3 class="pt-2 pb-1 text-sm font-semibold">Runtime</h3>
          <dl>
            <Row label="Status">
              <span class="inline-flex items-center gap-2">
                <StatusDot tone={serviceTone(s())} />
                {serviceLabel(s())}
                <Show when={s().message}>
                  <span class="text-muted-foreground">— {s().message}</span>
                </Show>
              </span>
            </Row>
            <Row label="Run">
              <span class="tabular">
                #{s().run || "—"} · {s().restarts} {s().restarts === 1 ? "restart" : "restarts"}
              </span>
            </Row>
            <Row label="PID">
              <span class="tabular">{s().pid || "—"}</span>
            </Row>
            <Row label="Started">{formatDateTime(s().startedAt)}</Row>
            <Show when={s().finishedAt}>
              <Row label="Finished">
                {formatDateTime(s().finishedAt)} · exit {s().exitCode}
              </Row>
            </Show>
            <Show when={s().status === "backoff" && s().nextRestartAt}>
              <Row label="Next restart">in {formatDuration(Math.max(0, s().nextRestartAt - now()))}</Row>
            </Show>
          </dl>
        </section>

        <section class="rounded-lg border bg-card px-4 py-2" aria-label="Readiness">
          <h3 class="pt-2 pb-1 text-sm font-semibold">Readiness</h3>
          <Show
            when={spec().ready}
            fallback={<p class="py-2 text-ui text-muted-foreground">No readiness probe; ready once started.</p>}
          >
            {(hc) => (
              <dl>
                <Row label="Health">
                  <Show when={s().health} fallback={<span class="text-muted-foreground">not running</span>}>
                    <HealthIndicator health={s().health} showLabel />
                  </Show>
                </Row>
                <Show when={s().healthDetail}>
                  <Row label="Last failure">
                    <pre class="max-h-40 overflow-auto rounded-sm bg-muted p-2 font-mono text-2xs whitespace-pre-wrap text-destructive">
                      {s().healthDetail}
                    </pre>
                  </Row>
                </Show>
                <Row label={hc().kind.toUpperCase()}>
                  <Code text={hc().target} />
                </Row>
                <Row label="Timing">
                  <span class="tabular">
                    every {formatDuration(hc().intervalMs)}, timeout {formatDuration(hc().timeoutMs)}, {hc().retries} retries,
                    start period {formatDuration(hc().startPeriodMs)}
                  </span>
                </Row>
              </dl>
            )}
          </Show>
        </section>
      </div>
    </div>
  );
}
