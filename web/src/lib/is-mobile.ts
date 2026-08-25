import { createSignal } from "solid-js";

/**
 * Reactive match on phone-sized viewports (< md). Drives the sidebar drawer
 * and mobile-only affordances; desktop layout is untouched.
 */
const query = window.matchMedia("(max-width: 767px)");
const [isMobile, setIsMobile] = createSignal(query.matches);

query.addEventListener("change", (e) => setIsMobile(e.matches));

export { isMobile };
