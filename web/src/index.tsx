import { render } from "solid-js/web";
import { App } from "./App";
import { TooltipProvider } from "./components/ui/tooltip";
import { ColorModeProvider, getClientColorMode } from "./components/color-mode";
import "./index.css";

// Apply the color mode class before first paint; the provider keeps it in
// sync on toggle.
document.documentElement.classList.toggle("dark", getClientColorMode() === "dark");

const root = document.getElementById("root");
if (root) {
  render(
    () => (
      <ColorModeProvider initialColorMode={getClientColorMode()}>
        <TooltipProvider>
          <App />
        </TooltipProvider>
      </ColorModeProvider>
    ),
    root,
  );
}
