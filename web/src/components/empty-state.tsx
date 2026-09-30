import { Show, type JSX } from "solid-js";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "~/components/ui/empty";

export function EmptyState(props: {
  icon?: (p: { class?: string }) => JSX.Element;
  title: string;
  description?: JSX.Element;
  children?: JSX.Element;
  class?: string;
}) {
  return (
    <Empty class={props.class}>
      <EmptyHeader>
        <Show when={props.icon}>
          {(icon) => {
            const Icon = icon();
            return (
              <EmptyMedia variant="icon">
                <Icon />
              </EmptyMedia>
            );
          }}
        </Show>
        <EmptyTitle>{props.title}</EmptyTitle>
        <Show when={props.description}>
          <EmptyDescription>{props.description}</EmptyDescription>
        </Show>
      </EmptyHeader>
      <Show when={props.children}>
        <EmptyContent>{props.children}</EmptyContent>
      </Show>
    </Empty>
  );
}
