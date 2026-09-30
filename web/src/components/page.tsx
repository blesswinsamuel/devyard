import { createEffect, onCleanup, Show, type JSX } from "solid-js";
import { Skeleton } from "~/components/ui/skeleton";
import { setPageTitle } from "~/data/crash";
import { cn } from "~/lib/utils";

/** Sets the document title prefix while the page is mounted. */
export function usePageTitle(title: () => string) {
  createEffect(() => setPageTitle(title()));
  onCleanup(() => setPageTitle(""));
}

export function Page(props: { children: JSX.Element; class?: string; wide?: boolean }) {
  return (
    <div class={cn("mx-auto flex w-full flex-col gap-6 px-4 py-5 md:px-6", !props.wide && "max-w-7xl", props.class)}>
      {props.children}
    </div>
  );
}

export function PageHeader(props: {
  title: JSX.Element;
  badges?: JSX.Element;
  meta?: JSX.Element;
  actions?: JSX.Element;
  children?: JSX.Element;
}) {
  return (
    <header class="flex flex-col gap-3">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex min-w-0 flex-col gap-1.5">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <h1 class="truncate text-xl font-semibold tracking-tight">{props.title}</h1>
            {props.badges}
          </div>
          <Show when={props.meta}>
            <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-ui text-muted-foreground">{props.meta}</div>
          </Show>
        </div>
        <Show when={props.actions}>
          <div class="flex flex-wrap items-center gap-1.5">{props.actions}</div>
        </Show>
      </div>
      {props.children}
    </header>
  );
}

export function Section(props: {
  title: string;
  count?: JSX.Element;
  actions?: JSX.Element;
  children: JSX.Element;
  class?: string;
  id?: string;
}) {
  return (
    <section class={cn("flex min-w-0 flex-col gap-2", props.class)} aria-labelledby={props.id}>
      <div class="flex min-h-7 items-center gap-2">
        <h2 id={props.id} class="text-sm font-semibold">
          {props.title}
        </h2>
        <Show when={props.count !== undefined}>
          <span class="tabular text-ui text-muted-foreground">{props.count}</span>
        </Show>
        <Show when={props.actions}>
          <div class="ml-auto flex items-center gap-1">{props.actions}</div>
        </Show>
      </div>
      {props.children}
    </section>
  );
}

/** A labelled value for meta rows and definition lists. */
export function Meta(props: { label: string; children: JSX.Element; class?: string }) {
  return (
    <span class={cn("inline-flex min-w-0 items-center gap-1.5", props.class)}>
      <span class="text-muted-foreground/80">{props.label}</span>
      <span class="min-w-0 truncate text-foreground">{props.children}</span>
    </span>
  );
}

export function PageSkeleton() {
  return (
    <Page>
      <div class="flex flex-col gap-2" aria-hidden="true">
        <Skeleton class="h-7 w-48" />
        <Skeleton class="h-4 w-80" />
      </div>
      <div class="flex flex-col gap-2" aria-hidden="true">
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-9 w-full" />
      </div>
      <span class="sr-only" role="status">
        Loading…
      </span>
    </Page>
  );
}
