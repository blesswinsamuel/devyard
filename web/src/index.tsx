import { render } from "solid-js/web";
import { App } from "./App";
import { TooltipProvider } from "./components/ui/tooltip";
import "./index.css";

const root = document.getElementById("root");
if (root) {
  render(
    () => (
      <TooltipProvider>
        <App />
      </TooltipProvider>
    ),
    root,
  );
}
