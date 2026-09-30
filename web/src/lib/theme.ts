import { createPersistedSignal, oneOf } from "./persistence";
import { prefersDark } from "./media";

export type ThemePref = "light" | "dark" | "system";

export const [themePref, setThemePref] = createPersistedSignal<ThemePref>(
  "theme",
  "system",
  oneOf("light", "dark", "system"),
);

export const resolvedTheme = (): "light" | "dark" => {
  const pref = themePref();
  if (pref === "system") return prefersDark() ? "dark" : "light";
  return pref;
};

const ORDER: ThemePref[] = ["light", "dark", "system"];

export function cycleTheme(): void {
  setThemePref((p) => ORDER[(ORDER.indexOf(p) + 1) % ORDER.length]!);
}
