import { A } from "@solidjs/router";
import { MapPinOff } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { EmptyState } from "~/components/empty-state";
import { paths } from "~/lib/paths";

export function NotFoundPage() {
  return (
    <EmptyState icon={MapPinOff} title="Page not found" description="There's nothing at this address." class="h-full">
      <Button as={A} href={paths.home()} variant="outline">
        Back to projects
      </Button>
    </EmptyState>
  );
}
