import { createEffect, createSignal, Show } from "solid-js";
import { createStore } from "solid-js/store";
import { Archive, GitCommitHorizontal, Pencil } from "lucide-solid";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import { shortcutKeys } from "~/lib/keyboard";
import { gitCommitFocus, setGitCommitFocus } from "~/app/ui-state";
import { getAction } from "~/data/actions";
import { getGit } from "~/data/entities";
import { actionBlocked, actionPending, runAction } from "~/app/runtime";
import { commitPending, commitStaged, fetchCommitMessage } from "./git-data";

const commitKeys = shortcutKeys("mod+Enter").join("");

/** Message drafts per project, kept while browsing other commits. */
const [drafts, setDrafts] = createStore<Record<string, string>>({});

/** Stash all changes through the action registry (shared with ⌘K). */
function StashButton(props: { project: string }) {
  const action = getAction("git.stash");
  const target = () => ({ kind: "project" as const, project: props.project });
  const pending = () => actionPending(action, target());
  return (
    <Button
      variant="ghost"
      size="xs"
      class="h-6 text-current hover:bg-background/60"
      disabled={pending() || actionBlocked(action)}
      aria-busy={pending() || undefined}
      onClick={() => void runAction(action, target())}
    >
      <Show when={pending()} fallback={<Archive />}>
        <Spinner />
      </Show>
      Stash
    </Button>
  );
}

export function CommitBox(props: { project: string; staged: number; unstaged: number }) {
  let textarea!: HTMLTextAreaElement;
  const [amend, setAmend] = createSignal(false);
  const message = () => drafts[props.project] ?? "";
  const pending = () => commitPending(props.project);
  // Amending needs a HEAD to rewrite; a normal commit needs a message and
  // something staged. While amending, an empty message keeps HEAD's message
  // and folds the staged changes in, so only one of the two is required.
  const canAmend = () => !!getGit(props.project)?.headHash;
  const canCommit = () =>
    !pending() && (amend() ? !!message().trim() || props.staged > 0 : !!message().trim() && props.staged > 0);

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

  // Turning amend on starts from HEAD's message when the box is empty.
  const toggleAmend = async () => {
    const next = !amend();
    setAmend(next);
    if (!next || message().trim()) return;
    const hash = getGit(props.project)?.headHash;
    if (!hash) return;
    try {
      const previous = await fetchCommitMessage(props.project, hash);
      if (previous && !message().trim()) setDrafts(props.project, previous);
    } catch {
      // Keep the empty draft; the user can still type a new message.
    }
  };

  const submit = async (e?: Event) => {
    e?.preventDefault();
    if (!canCommit()) return;
    const amending = amend();
    if (await commitStaged(props.project, message().trim(), amending)) {
      setDrafts(props.project, "");
      setAmend(false);
    }
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
        <span class="ml-auto flex items-center gap-1">
          <Button
            variant={amend() ? "secondary" : "ghost"}
            size="xs"
            class="h-6"
            aria-pressed={amend()}
            disabled={!canAmend() || pending()}
            title={
              canAmend()
                ? "Amend HEAD: fold the staged changes in and replace its message"
                : "No HEAD commit to amend"
            }
            onClick={() => void toggleAmend()}
          >
            <Pencil />
            Amend
          </Button>
          <StashButton project={props.project} />
        </span>
      </div>
      <div class="flex flex-col gap-2 @lg:flex-row @lg:items-stretch">
        <Textarea
          ref={textarea}
          rows={2}
          class="min-h-14 flex-1 resize-y font-mono text-xs"
          placeholder={
            amend()
              ? "Amend message (empty keeps HEAD's message)"
              : props.staged
                ? `Commit message (${commitKeys} to commit)`
                : "Stage changes, then write a commit message"
          }
          aria-label={amend() ? "Amend message" : "Commit message"}
          value={message()}
          onInput={(e) => setDrafts(props.project, e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void submit(e);
          }}
        />
        <Button
          type="submit"
          variant={amend() ? "secondary" : "default"}
          class="self-end @lg:h-auto @lg:self-stretch"
          disabled={!canCommit()}
          aria-busy={pending() || undefined}
          title={
            amend()
              ? "Amend HEAD"
              : props.staged
                ? `Commit (${commitKeys})`
                : "Nothing staged"
          }
        >
          <Show when={pending()} fallback={<GitCommitHorizontal />}>
            <Spinner />
          </Show>
          {amend() ? "Amend" : "Commit"}
        </Button>
      </div>
    </form>
  );
}
