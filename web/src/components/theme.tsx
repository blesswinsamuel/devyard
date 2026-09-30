import { createEffect, type ParentProps } from "solid-js";
import { ColorModeProvider, useColorMode } from "~/components/color-mode";
import { resolvedTheme } from "~/lib/theme";

function ThemeSync() {
  const { setColorMode } = useColorMode();
  createEffect(() => {
    const mode = resolvedTheme();
    const html = document.documentElement;
    html.classList.toggle("dark", mode === "dark");
    html.classList.toggle("light", mode === "light");
    setColorMode(mode);
  });
  return null;
}

/** Light/dark/system theme; feeds zaidan's color-mode context (toasts). */
export function ThemeProvider(props: ParentProps) {
  return (
    <ColorModeProvider initialColorMode={resolvedTheme()}>
      <ThemeSync />
      {props.children}
    </ColorModeProvider>
  );
}
