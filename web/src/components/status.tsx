import { Show, type JSX } from "solid-js";
import { Heart, HeartCrack, HeartPulse } from "lucide-solid";
import { Badge } from "~/components/ui/badge";
import { cn } from "~/lib/utils";
import { isTransitional, toneBadge, toneBg, toneText, type Tone } from "~/lib/status";
import type { HealthStatus } from "~/data/entities";

/** Small coloured dot; pulses while transitional. Decorative: pair with text. */
export function StatusDot(props: { tone: Tone; pulse?: boolean; class?: string; label?: string }) {
  return (
    <span
      class={cn("relative inline-flex size-2 shrink-0 rounded-full", toneBg[props.tone], props.class)}
      role={props.label ? "img" : undefined}
      aria-label={props.label}
      aria-hidden={props.label ? undefined : true}
    >
      <Show when={props.pulse}>
        <span class={cn("absolute inset-0 animate-ping rounded-full opacity-60", toneBg[props.tone])} />
      </Show>
    </span>
  );
}

export function StatusBadge(props: { tone: Tone; status: string; label?: string; class?: string }) {
  return (
    <Badge variant={toneBadge[props.tone]} class={cn("gap-1.5 font-medium", props.class)}>
      <StatusDot tone={props.tone} pulse={isTransitional(props.status)} />
      {props.label ?? props.status}
    </Badge>
  );
}

const HEALTH: Record<Exclude<HealthStatus, "">, { tone: Tone; icon: (p: { class?: string }) => JSX.Element }> = {
  healthy: { tone: "success", icon: Heart },
  starting: { tone: "warning", icon: HeartPulse },
  unhealthy: { tone: "danger", icon: HeartCrack },
};

/** Health indicator; renders nothing without a healthcheck. */
export function HealthIndicator(props: { health: HealthStatus; showLabel?: boolean; detail?: string; class?: string }) {
  return (
    <Show when={props.health !== "" && HEALTH[props.health as keyof typeof HEALTH]}>
      {(h) => {
        const Icon = h().icon;
        return (
          <span
            class={cn("inline-flex items-center gap-1", toneText[h().tone], props.class)}
            title={props.detail || `health: ${props.health}`}
          >
            <Icon class="size-3.5" aria-hidden="true" />
            <Show when={props.showLabel} fallback={<span class="sr-only">{props.health}</span>}>
              <span>{props.health}</span>
            </Show>
          </span>
        );
      }}
    </Show>
  );
}
