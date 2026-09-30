import { createRoot, createSignal, type Accessor } from "solid-js";

function mediaSignal(query: string): Accessor<boolean> {
  return createRoot(() => {
    if (typeof window === "undefined" || !window.matchMedia) return () => false;
    const mql = window.matchMedia(query);
    const [matches, setMatches] = createSignal(mql.matches);
    mql.addEventListener?.("change", (e) => setMatches(e.matches));
    return matches;
  });
}

/** Phone-sized viewports (< md): sidebar and dock become sheets, tables become cards. */
export const isMobile = mediaSignal("(max-width: 767px)");
/** Room for multi-pane layouts (lg and up): the git view shows panes side by side. */
export const isWide = mediaSignal("(min-width: 1024px)");
export const prefersDark = mediaSignal("(prefers-color-scheme: dark)");
