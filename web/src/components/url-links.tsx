import { For, Show } from "solid-js";
import { ExternalLink } from "lucide-solid";
import { displayUrl } from "~/lib/format";
import { cn } from "~/lib/utils";

/** Service URLs as compact external links; overflows into "+N". */
export function UrlLinks(props: { urls: string[]; max?: number; class?: string }) {
  const max = () => props.max ?? 2;
  return (
    <span class={cn("inline-flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5", props.class)}>
      <For each={props.urls.slice(0, max())}>
        {(url) => (
          <a
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-w-0 items-center gap-1 text-primary hover:underline focus-ring rounded-sm"
            onClick={(e) => e.stopPropagation()}
          >
            <span class="truncate">{displayUrl(url)}</span>
            <ExternalLink class="size-3 shrink-0 opacity-70" aria-hidden="true" />
          </a>
        )}
      </For>
      <Show when={props.urls.length > max()}>
        <span class="text-muted-foreground" title={props.urls.slice(max()).join("\n")}>
          +{props.urls.length - max()}
        </span>
      </Show>
    </span>
  );
}
