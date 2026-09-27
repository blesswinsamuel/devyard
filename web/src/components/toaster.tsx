import { AlertTriangle, CheckCircle2, Info, LoaderCircle, OctagonX } from "lucide-solid";
import type { Component, ComponentProps, JSX } from "solid-js";
import { Toaster as Sonner } from "solid-sonner";
import { theme } from "~/stores/app";

type ToasterProps = ComponentProps<typeof Sonner>;

const Toaster: Component<ToasterProps> = (props) => {
  return (
    <Sonner
      theme={theme()}
      class="toaster group"
      position="top-right"
      visibleToasts={5}
      duration={5000}
      icons={{
        success: <CheckCircle2 class="size-4 text-success" />,
        info: <Info class="size-4 text-info" />,
        warning: <AlertTriangle class="size-4 text-warning" />,
        error: <OctagonX class="size-4 text-destructive" />,
        loading: <LoaderCircle class="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as JSX.CSSProperties
      }
      {...props}
    />
  );
};

export { Toaster };
