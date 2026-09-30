import { Show } from "solid-js";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
import { confirmRequest, setConfirmRequest } from "~/app/ui-state";

/** The single AlertDialog behind `confirm()`. */
export function ConfirmHost() {
  const close = (ok: boolean) => {
    const req = confirmRequest();
    setConfirmRequest(null);
    req?.resolve(ok);
  };
  return (
    <AlertDialog open={!!confirmRequest()} onOpenChange={(open) => !open && close(false)}>
      <Show when={confirmRequest()}>
        {(req) => (
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{req().title}</AlertDialogTitle>
              <AlertDialogDescription>{req().description}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction variant={req().destructive ? "destructive" : "default"} onClick={() => close(true)}>
                {req().confirmLabel}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        )}
      </Show>
    </AlertDialog>
  );
}
