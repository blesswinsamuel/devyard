import * as ContextMenuPrimitive from "@kobalte/core/context-menu";
import type { PolymorphicProps } from "@kobalte/core/polymorphic";
import { Check, ChevronRight } from "lucide-solid";
import type { ComponentProps, ValidComponent } from "solid-js";
import { mergeProps, splitProps } from "solid-js";
import { cn } from "~/lib/utils";

type ContextMenuProps = ContextMenuPrimitive.ContextMenuRootProps;

const ContextMenu = (props: ContextMenuProps) => {
  return <ContextMenuPrimitive.Root data-slot="context-menu" {...props} />;
};

type ContextMenuPortalProps = ContextMenuPrimitive.ContextMenuPortalProps;

const ContextMenuPortal = (props: ContextMenuPortalProps) => {
  return <ContextMenuPrimitive.Portal data-slot="context-menu-portal" {...props} />;
};

type ContextMenuTriggerProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuTriggerProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuTrigger = <T extends ValidComponent = "div">(
  props: ContextMenuTriggerProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuTriggerProps, ["class"]);
  return (
    <ContextMenuPrimitive.Trigger
      class={local.class}
      data-slot="context-menu-trigger"
      {...others}
    />
  );
};

type ContextMenuContentProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuContentProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuContent = <T extends ValidComponent = "div">(
  props: ContextMenuContentProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuContentProps, ["class"]);
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        data-slot="context-menu-content"
        class={cn(
          "z-50 z-context-menu-content z-menu-target max-h-(--kb-popper-available-height) min-w-36 origin-(--kb-menu-content-transform-origin) overflow-y-auto overflow-x-hidden outline-none data-closed:overflow-hidden",
          local.class,
        )}
        {...others}
      />
    </ContextMenuPrimitive.Portal>
  );
};

type ContextMenuGroupProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuGroupProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuGroup = <T extends ValidComponent = "div">(props: ContextMenuGroupProps<T>) => {
  const [local, others] = splitProps(props as ContextMenuGroupProps, ["class"]);
  return (
    <ContextMenuPrimitive.Group class={local.class} data-slot="context-menu-group" {...others} />
  );
};

type ContextMenuLabelProps<T extends ValidComponent = "span"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuGroupLabelProps<T>
> &
  Pick<ComponentProps<T>, "class"> & {
    inset?: boolean;
  };

const ContextMenuLabel = <T extends ValidComponent = "span">(props: ContextMenuLabelProps<T>) => {
  const [local, others] = splitProps(props as ContextMenuLabelProps, ["class", "inset"]);
  return (
    <ContextMenuPrimitive.GroupLabel
      data-slot="context-menu-label"
      data-inset={local.inset}
      class={cn("z-context-menu-label data-inset:pl-8", local.class)}
      {...others}
    />
  );
};

type ContextMenuItemProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuItemProps<T>
> &
  Pick<ComponentProps<T>, "class"> & {
    inset?: boolean;
    variant?: "default" | "destructive";
  };

const ContextMenuItem = <T extends ValidComponent = "div">(rawProps: ContextMenuItemProps<T>) => {
  const props = mergeProps({ variant: "default" } as ContextMenuItemProps<T>, rawProps);
  const [local, others] = splitProps(props as ContextMenuItemProps, ["class", "inset", "variant"]);
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      data-inset={local.inset}
      data-variant={local.variant}
      class={cn(
        "group/context-menu-item relative z-context-menu-item flex cursor-default select-none items-center outline-hidden data-disabled:pointer-events-none data-inset:pl-8 data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
        local.class,
      )}
      {...others}
    />
  );
};

type ContextMenuSubProps = ContextMenuPrimitive.ContextMenuSubProps;

const ContextMenuSub = (props: ContextMenuSubProps) => {
  return <ContextMenuPrimitive.Sub data-slot="context-menu-sub" {...props} />;
};

type ContextMenuSubTriggerProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuSubTriggerProps<T>
> &
  Pick<ComponentProps<T>, "class" | "children"> & {
    inset?: boolean;
  };

const ContextMenuSubTrigger = <T extends ValidComponent = "div">(
  props: ContextMenuSubTriggerProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuSubTriggerProps, [
    "class",
    "inset",
    "children",
  ]);
  return (
    <ContextMenuPrimitive.SubTrigger
      data-slot="context-menu-sub-trigger"
      data-inset={local.inset}
      class={cn(
        "z-context-menu-sub-trigger flex cursor-default select-none items-center outline-hidden data-inset:pl-8 [&_svg]:pointer-events-none [&_svg]:shrink-0",
        local.class,
      )}
      {...others}
    >
      {local.children}
      <ChevronRight class="ml-auto" />
    </ContextMenuPrimitive.SubTrigger>
  );
};

type ContextMenuSubContentProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuSubContentProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuSubContent = <T extends ValidComponent = "div">(
  props: ContextMenuSubContentProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuSubContentProps, ["class"]);
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.SubContent
        data-slot="context-menu-sub-content"
        class={cn(
          "z-50 z-context-menu-sub-content z-menu-target max-h-(--kb-popper-available-height) min-w-32 origin-(--kb-menu-content-transform-origin) overflow-y-auto overflow-x-hidden outline-none data-closed:overflow-hidden",
          local.class,
        )}
        {...others}
      />
    </ContextMenuPrimitive.Portal>
  );
};

type ContextMenuCheckboxItemProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuCheckboxItemProps<T>
> &
  Pick<ComponentProps<T>, "class" | "children">;

const ContextMenuCheckboxItem = <T extends ValidComponent = "div">(
  props: ContextMenuCheckboxItemProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuCheckboxItemProps, ["class", "children"]);
  return (
    <ContextMenuPrimitive.CheckboxItem
      data-slot="context-menu-checkbox-item"
      class={cn(
        "relative z-context-menu-checkbox-item flex cursor-default select-none items-center outline-hidden data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
        local.class,
      )}
      {...others}
    >
      <span
        class="pointer-events-none z-context-menu-item-indicator"
        data-slot="context-menu-checkbox-item-indicator"
      >
        <ContextMenuPrimitive.ItemIndicator>
          <Check />
        </ContextMenuPrimitive.ItemIndicator>
      </span>
      {local.children}
    </ContextMenuPrimitive.CheckboxItem>
  );
};

type ContextMenuRadioGroupProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuRadioGroupProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuRadioGroup = <T extends ValidComponent = "div">(
  props: ContextMenuRadioGroupProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuRadioGroupProps, ["class"]);
  return (
    <ContextMenuPrimitive.RadioGroup
      class={local.class}
      data-slot="context-menu-radio-group"
      {...others}
    />
  );
};

type ContextMenuRadioItemProps<T extends ValidComponent = "div"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuRadioItemProps<T>
> &
  Pick<ComponentProps<T>, "class" | "children">;

const ContextMenuRadioItem = <T extends ValidComponent = "div">(
  props: ContextMenuRadioItemProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuRadioItemProps, ["class", "children"]);
  return (
    <ContextMenuPrimitive.RadioItem
      data-slot="context-menu-radio-item"
      class={cn(
        "relative z-context-menu-radio-item flex cursor-default select-none items-center outline-hidden data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
        local.class,
      )}
      {...others}
    >
      <span
        class="pointer-events-none z-context-menu-item-indicator"
        data-slot="context-menu-radio-item-indicator"
      >
        <ContextMenuPrimitive.ItemIndicator>
          <Check />
        </ContextMenuPrimitive.ItemIndicator>
      </span>
      {local.children}
    </ContextMenuPrimitive.RadioItem>
  );
};

type ContextMenuSeparatorProps<T extends ValidComponent = "hr"> = PolymorphicProps<
  T,
  ContextMenuPrimitive.ContextMenuSeparatorProps<T>
> &
  Pick<ComponentProps<T>, "class">;

const ContextMenuSeparator = <T extends ValidComponent = "hr">(
  props: ContextMenuSeparatorProps<T>,
) => {
  const [local, others] = splitProps(props as ContextMenuSeparatorProps, ["class"]);
  return (
    <ContextMenuPrimitive.Separator
      data-slot="context-menu-separator"
      class={cn("z-context-menu-separator", local.class)}
      {...others}
    />
  );
};

type ContextMenuShortcutProps = ComponentProps<"span">;

const ContextMenuShortcut = (props: ContextMenuShortcutProps) => {
  const [local, others] = splitProps(props, ["class"]);
  return (
    <span
      data-slot="context-menu-shortcut"
      class={cn("z-context-menu-shortcut", local.class)}
      {...others}
    />
  );
};

export {
  ContextMenu,
  ContextMenuCheckboxItem,
  ContextMenuContent,
  ContextMenuGroup,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuPortal,
  ContextMenuRadioGroup,
  ContextMenuRadioItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
};
