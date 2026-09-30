import { createMemo, Show } from "solid-js";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";

const MAX_RUNS = 20;

/** Picks which run's logs to show, as an offset from the current run. */
export function RunSelect(props: { run: number; value: number; onChange: (offset: number) => void }) {
  const options = createMemo(() => Array.from({ length: Math.min(props.run, MAX_RUNS) }, (_, i) => -i));
  const label = (offset: number) => (offset === 0 ? `Run #${props.run} · current` : `Run #${props.run + offset}`);
  return (
    <Show when={props.run > 1}>
      <Select<number>
        options={options()}
        value={props.value}
        onChange={(v) => v !== null && props.onChange(v)}
        itemComponent={(p) => <SelectItem item={p.item}>{label(p.item.rawValue)}</SelectItem>}
      >
        <SelectTrigger size="sm" class="h-7 text-ui" aria-label="Run to show">
          <SelectValue<number>>{(s) => label(s.selectedOption() ?? 0)}</SelectValue>
        </SelectTrigger>
        <SelectContent />
      </Select>
    </Show>
  );
}
