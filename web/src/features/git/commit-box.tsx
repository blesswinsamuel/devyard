import { createEffect, Show } from "solid-js";
import { createStore } from "solid-js/store";
import { GitCommitHorizontal } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { shortcutKeys } from "~/lib/keyboard";
import { gitCommitFocus, setGitCommitFocus } from "~/app/ui-state";
import { commitPending, commitStaged } from "./git-data";

const commitKeys = shortcutKeys("mod+Enter").join("");

/** Message drafts per project, kept while browsing other commits. */
const [drafts, setDrafts] = createStore<Record<string, string>>({});

export function CommitBox(props: { project: string; staged: number; unstaged: number }) {
  let textarea!: HTMLTextAreaElement;
  const message = () => drafts[props.project] ?? "";
  const pending = () => commitPending(props.project);
  const canCommit = () => !!message().trim() && props.staged > 0 && !pending();

  // "Commit…" from the palette lands here with the message focused, once the
  // palette has closed and handed focus back.
  createEffect(() => {
    if (gitCommitFocus() !== props.project) return;
    setGitCommitFocus(null);
    // The dialog restores focus (to the body) on a timer after it unmounts:
    // wait for it to go, then keep the focus for a few frames.
    let frames = 0;
    const focus = () => {
      frames++;
      if (document.querySelector('[role="dialog"]')) {
        if (frames < 60) requestAnimationFrame(focus);
        return;
      }
      if (document.activeElement !== textarea) {
        if (document.activeElement && document.activeElement !== document.body) return;
        textarea.focus();
      }
      if (frames < 75) requestAnimationFrame(focus);
    };
    focus();
  });

  const submit = async (e?: Event) => {
    e?.preventDefault();
    if (!canCommit()) return;
    if (await commitStaged(props.project, message().trim())) setDrafts(props.project, "");
  };

  return (
    <form class="@container flex flex-col gap-2" onSubmit={submit} aria-label="Commit staged changes">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span class="inline-flex items-center gap-1.5 text-sm font-semibold text-warning">
          <GitCommitHorizontal class="size-4" />
          Uncommitted changes
        </span>
        <span class="tabular text-2xs text-muted-foreground">
          {props.staged} staged · {props.unstaged} not staged
        </span>
      </div>
      <div class="flex flex-col gap-2 @lg:flex-row @lg:items-stretch">
        <Textarea
          ref={textarea}
          rows={2}
          class="min-h-14 flex-1 resize-y font-mono text-xs"
          placeholder={props.staged ? `Commit message (${commitKeys} to commit)` : "Stage changes, then write a commit message"}
          aria-label="Commit message"
          value={message()}
          onInput={(e) => setDrafts(props.project, e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void submit(e);
          }}
        />
        <Button
          type="submit"
          class="self-end @lg:h-auto @lg:self-stretch"
          disabled={!canCommit()}
          aria-busy={pending() || undefined}
          title={props.staged ? `Commit (${commitKeys})` : "Nothing staged"}
        >
          <Show when={pending()} fallback={<GitCommitHorizontal />}>
            <Spinner />
          </Show>
          Commit
        </Button>
      </div>
    </form>
  );
}
