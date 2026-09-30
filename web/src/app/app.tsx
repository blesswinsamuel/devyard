import { Router } from "@solidjs/router";
import { QueryClientProvider } from "@tanstack/solid-query";
import { Toaster } from "~/components/ui/toast";
import { TooltipProvider } from "~/components/ui/tooltip";
import { ThemeProvider } from "~/components/theme";
import { queryClient } from "~/data/queries";
import { routes } from "./routes";
import { Shell } from "./shell";

export function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider delay={400}>
          <Router root={Shell}>{routes}</Router>
          <Toaster position="bottom-right" closeButton visibleToasts={4} />
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
