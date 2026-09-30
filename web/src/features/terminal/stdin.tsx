import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { CornerDownLeft } from "lucide-solid";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "~/components/ui/input-group";
import { AttachSession, type SessionTarget } from "~/data/sessions";

/** One-line stdin for non-TTY sessions: sends the line plus "\n"; EOF closes stdin. */
export function LineInput(props: {
  onSend: (line: string) => void;
  onEof?: () => void;
  disabled?: boolean;
  placeholder?: string;
}) {
  const [value, setValue] = createSignal("");
  const send = () => {
    props.onSend(`${value()}\n`);
    setValue("");
  };
  return (
    <form
      class="border-t bg-card p-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      <InputGroup class="h-8">
        <InputGroupAddon>
          <span class="font-mono text-xs text-muted-foreground">stdin</span>
        </InputGroupAddon>
        <InputGroupInput
          class="font-mono text-xs"
          placeholder={props.placeholder ?? "Type a line and press Enter"}
          aria-label="Standard input"
          value={value()}
          disabled={props.disabled}
          onInput={(e) => setValue(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.ctrlKey && e.key === "d" && !value()) {
              e.preventDefault();
              props.onEof?.();
            }
          }}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupButton type="submit" size="icon-xs" aria-label="Send line" disabled={props.disabled}>
            <CornerDownLeft />
          </InputGroupButton>
          <Show when={props.onEof}>
            <InputGroupButton
              size="xs"
              class="font-mono"
              title="Close the process's standard input (Ctrl-D)"
              disabled={props.disabled}
              onClick={() => props.onEof?.()}
            >
              EOF
            </InputGroupButton>
          </Show>
        </InputGroupAddon>
      </InputGroup>
    </form>
  );
}

/** Stdin-only attach for non-TTY tasks whose output is shown by a log viewer.
 * Renders nothing when the process doesn't accept input. */
export function StdinBar(props: { target: SessionTarget }) {
  const [ready, setReady] = createSignal(false);
  const [accepts, setAccepts] = createSignal(true);
  const [closed, setClosed] = createSignal(false);
  let session: AttachSession | undefined;
  onMount(() => {
    session = new AttachSession(
      props.target,
      { cols: 120, rows: 30 },
      {
        onReady: (info) => {
          setAccepts(info.stdin && !info.tty);
          setReady(true);
        },
        onPhase: (p) => p !== "ready" && setReady(false),
      },
    );
  });
  onCleanup(() => session?.detach());
  return (
    <Show when={accepts()}>
      <LineInput
        disabled={!ready() || closed()}
        placeholder={closed() ? "stdin closed" : ready() ? undefined : "Connecting stdin…"}
        onSend={(l) => session?.input(l)}
        onEof={() => {
          session?.eof();
          setClosed(true);
        }}
      />
    </Show>
  );
}
