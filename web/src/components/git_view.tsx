import { For, Show, createMemo, createSignal } from "solid-js";
import { ArrowLeft, GitBranch, Loader2, RefreshCw } from "lucide-solid";
import {
  selectedProject,
  gitCommits,
  gitError,
  gitLoading,
  closeGitView,
  loadGitLog,
} from "~/store";
import type { GitCommit } from "~/types";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";

function formatAuthorTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function CommitMeta(props: { commit: GitCommit }) {
  const hash = () => props.commit.hash;
  const shortUrl = () => props.commit.short;

  const [copied, setCopied] = createSignal(false);

  const copy = () => {
    if (!hash()) return;
    void navigator.clipboard.writeText(hash()).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <div class="flex items-center gap-2 font-mono text-xs text-muted-foreground">
      <Show when={props.commit.head}>
        <Badge variant="secondary" class="font-sans">
          <GitBranch class="mr-1 size-3" />
          HEAD
        </Badge>
      </Show>
      <span class="truncate">{shortUrl()}</span>
      <Show when={props.commit.parents && props.commit.parents.length > 1}>
        <span class="shrink-0 text-muted-foreground/70">
          (+{props.commit.parents!.length - 1})
        </span>
      </Show>
      <span class="shrink-0 text-muted-foreground/50">·</span>
      <span class="truncate">{formatAuthorTime(props.commit.time)}</span>
      <span class="truncate">{props.commit.author}</span>
      <button
        type="button"
        onClick={copy}
        class="shrink-0 rounded px-1 text-muted-foreground/50 hover:text-foreground"
        title="Copy full hash"
      >
        {copied() ? "copied" : "copy"}
      </button>
    </div>
  );
}

export function GitView() {
  const project = () => selectedProject();
  const commits = createMemo(() => (project() ? gitCommits()[project()!] ?? [] : []));
  const error = createMemo(() => (project() ? gitError()[project()!] ?? "" : ""));
  const loading = createMemo(() => (project() ? !!gitLoading()[project()!] : false));

  return (
    <div class="flex h-full flex-col min-w-0 overflow-hidden">
      <div class="flex h-10 shrink-0 items-center gap-2 border-b border-border px-4">
        <Button
          variant="ghost"
          size="sm"
          class="gap-1.5 text-muted-foreground hover:text-foreground"
          onClick={closeGitView}
        >
          <ArrowLeft class="size-4" />
          <span class="hidden sm:inline">Back</span>
        </Button>
        <div class="flex min-w-0 items-center gap-2">
          <GitBranch class="size-4 shrink-0 text-muted-foreground" />
          <span class="truncate font-semibold">{project()}</span>
          <span class="truncate text-xs text-muted-foreground">/ git log</span>
        </div>
        <div class="ml-auto">
          <Button
            variant="ghost"
            size="icon"
            class="size-8 text-muted-foreground hover:text-foreground"
            onClick={() => project() && loadGitLog(project()!)}
            title="Refresh"
          >
            <RefreshCw class="size-4" />
            <span class="sr-only">Refresh</span>
          </Button>
        </div>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto">
        <Show
          when={!loading()}
          fallback={
            <div class="flex h-full items-center justify-center gap-2 text-muted-foreground">
              <Loader2 class="size-4 animate-spin" />
              <span class="text-sm">Loading git log…</span>
            </div>
          }
        >
          <Show when={!error()} fallback={<ErrorState message={error()} onRetry={() => project() && loadGitLog(project()!)} />}>
            <Show
              when={commits().length > 0}
              fallback={
                <div class="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground p-6">
                  <GitBranch class="size-8 stroke-[1.5]" />
                  <p class="text-sm">No commits found in this repository.</p>
                </div>
              }
            >
              <ul class="divide-y divide-border">
                <For each={commits()}>
                  {(c) => (
                    <li class="flex flex-col gap-1 px-4 py-3">
                      <div class="flex min-w-0 items-start gap-2">
                        <span
                          class="mt-0.5 size-2 shrink-0 rounded-full bg-border"
                          aria-hidden="true"
                        />
                        <span class="min-w-0 text-sm leading-snug">{c.subject}</span>
                      </div>
                      <div class="pl-4">
                        <CommitMeta commit={c} />
                      </div>
                    </li>
                  )}
                </For>
              </ul>
            </Show>
          </Show>
        </Show>
      </div>
    </div>
  );
}

function ErrorState(props: { message: string; onRetry: () => void }) {
  return (
    <div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
      <GitBranch class="size-8 stroke-[1.5] text-muted-foreground" />
      <p class="text-sm text-muted-foreground">{props.message}</p>
      <Button variant="outline" size="sm" class="gap-1.5" onClick={props.onRetry}>
        <RefreshCw class="size-3.5" />
        Retry
      </Button>
    </div>
  );
}
