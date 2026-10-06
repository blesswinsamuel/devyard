import { useInfiniteQuery, useQuery } from "@tanstack/solid-query";
import { toast } from "solid-sonner";
import type { GitBranch, GitCommit, GitDiffResult, GitFileChange, GitLogResponse, GitStash } from "~/gen/devyard/v1/control_pb";
import { api } from "~/data/client";
import { errorDescription, isCanceled } from "~/data/errors";
import { isPending, withPending } from "~/data/pending";
import { queryClient, queryKeys } from "~/data/queries";

/** Hash of the pseudo-commit for uncommitted changes (GitLog / GitDiff). */
export const WORKDIR = "WORKDIR";

/** Context lines of a unified diff; the largest step shows whole files. */
export const CONTEXT_STEPS = [3, 10, 25, 100, 100_000] as const;
export const DEFAULT_CONTEXT = CONTEXT_STEPS[0];
export const FULL_CONTEXT = CONTEXT_STEPS[CONTEXT_STEPS.length - 1]!;

export interface GitRefView {
  name: string;
  /** "branch" | "remote" | "tag" | "stash" | "head" */
  type: string;
  isActive: boolean;
}

export interface GitCommitView {
  hash: string;
  short: string;
  author: string;
  email: string;
  time: number;
  parents: string[];
  subject: string;
  /** Message below the subject; only GitDiff resolves it (empty in the log). */
  body: string;
  head: boolean;
  refs: GitRefView[];
  additions: number;
  deletions: number;
  filesChanged: number;
}

export interface GitBranchView {
  name: string;
  hash: string;
  isActive: boolean;
  isRemote: boolean;
  upstream: string;
  ahead: number;
  behind: number;
}

export interface GitTagView {
  name: string;
  hash: string;
}

export interface GitStashView {
  index: string;
  name: string;
  hash: string;
  time: number;
}

export interface GitFileView {
  path: string;
  oldPath: string;
  status: string;
  additions: number;
  deletions: number;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
}

export interface GitLogView {
  commits: GitCommitView[];
  branches: GitBranchView[];
  tags: GitTagView[];
  stashes: GitStashView[];
}

/** One page of the log; the client fetches older pages on demand. */
export interface GitLogPage extends GitLogView {
  hasMore: boolean;
}

/** Flattens an infinite query's pages into one view for the panes. */
export function combineLog(pages: GitLogPage[] | undefined): GitLogView & { hasMore: boolean } {
  const list = pages ?? [];
  return {
    commits: list.flatMap((p) => p.commits),
    // Branches, tags and stashes are repository-level: the first page has them.
    branches: list[0]?.branches ?? [],
    tags: list[0]?.tags ?? [],
    stashes: list[0]?.stashes ?? [],
    hasMore: list.length > 0 && !!list[list.length - 1]?.hasMore,
  };
}

export interface GitDiffView {
  hash: string;
  contextLines: number;
  commit: GitCommitView | null;
  files: GitFileView[];
  diff: string;
}

// The daemon fills `body` in GitDiff only (the log listing leaves it empty).
export const toCommit = (c: GitCommit): GitCommitView => ({
  hash: c.hash,
  short: c.short,
  author: c.author,
  email: c.email,
  time: Number(c.timeUnixMs),
  parents: [...c.parents],
  subject: c.subject,
  body: c.body,
  head: c.head,
  refs: c.refs.map((r) => ({ name: r.name, type: r.type, isActive: r.isActive })),
  additions: c.additions,
  deletions: c.deletions,
  filesChanged: c.filesChanged,
});

const toBranch = (b: GitBranch): GitBranchView => ({
  name: b.name,
  hash: b.hash,
  isActive: b.isActive,
  isRemote: b.isRemote,
  upstream: b.upstream,
  ahead: b.ahead,
  behind: b.behind,
});

const toStash = (s: GitStash): GitStashView => ({ index: s.index, name: s.name, hash: s.hash, time: Number(s.timeUnixMs) });

const toFile = (f: GitFileChange): GitFileView => ({
  path: f.path,
  oldPath: f.oldPath,
  status: f.status,
  additions: f.additions,
  deletions: f.deletions,
  staged: f.staged,
  unstaged: f.unstaged,
  untracked: f.untracked,
});

export function toLog(res: GitLogResponse): GitLogPage {
  return {
    commits: res.commits.map(toCommit),
    branches: res.branches.map(toBranch),
    tags: res.tags.map((t) => ({ name: t.name, hash: t.hash })),
    stashes: res.stashes.map(toStash),
    hasMore: res.hasMore,
  };
}

export function toDiff(hash: string, contextLines: number, r: GitDiffResult | undefined): GitDiffView {
  return {
    hash,
    contextLines,
    commit: r?.commit ? toCommit(r.commit) : null,
    files: (r?.files ?? []).map(toFile),
    diff: r?.diff ?? "",
  };
}

/**
 * Commits, branches, tags and stashes; refetched whenever change_seq moves.
 * With `path`, only the commits that touched that file are listed (its
 * history), and the WORKDIR pseudo-commit is absent.
 */
export function useGitLog(project: () => string, path: () => string | undefined = () => undefined) {
  return useInfiniteQuery(() => {
    const p = path();
    return {
      queryKey: p ? queryKeys.gitLogPath(project(), p) : queryKeys.gitLog(project()),
      queryFn: async ({ pageParam, signal }: { pageParam: number; signal: AbortSignal }) =>
        toLog(await api.gitLog({ project: project(), skip: pageParam, path: p ?? "" }, { signal })),
      initialPageParam: 0,
      // Skip past every real commit already loaded; WORKDIR is page-local.
      getNextPageParam: (last: GitLogPage, pages: GitLogPage[]) =>
        last.hasMore ? pages.reduce((n, p) => n + p.commits.filter((c) => c.hash !== WORKDIR).length, 0) : undefined,
    };
  });
}

/**
 * The diff of one commit, or of the working tree for WORKDIR. While context
 * lines change, the previous diff of the same commit stays on screen. A
 * `path` narrows a commit's diff to one file (its history view).
 */
export function useGitDiff(
  project: () => string,
  hash: () => string | undefined,
  contextLines: () => number,
  path: () => string = () => "",
) {
  return useQuery(() => {
    const h = hash() ?? "";
    const ctx = contextLines();
    const p = path();
    const workdir = h === WORKDIR;
    return {
      queryKey: workdir ? queryKeys.gitWorkdirDiff(project(), ctx) : queryKeys.gitCommitDiff(project(), h, ctx, p),
      queryFn: async ({ signal }: { signal: AbortSignal }) =>
        toDiff(h, ctx, (await api.gitDiff({ project: project(), hash: h, contextLines: ctx, path: p }, { signal })).result),
      enabled: !!h,
      // Commits are immutable (cache forever); the working tree is refetched
      // on change_seq bumps, so a short staleness window only saves tab
      // switches from respawning git.
      staleTime: workdir ? 2_000 : Infinity,
      placeholderData: (prev: GitDiffView | undefined) => (prev?.hash === h ? prev : undefined),
    };
  });
}

function toastFailure(title: string, err: unknown) {
  if (!isCanceled(err)) toast.error(title, { description: errorDescription(err) });
}

const stageKey = (project: string, path: string, unstage: boolean) => `git.stage|${project}|${unstage ? "-" : "+"}|${path}`;

export const stagePending = (project: string, path: string, unstage: boolean) => isPending(stageKey(project, path, unstage));

/** Stages (or unstages) one path of the working tree. */
export function stagePath(project: string, path: string, unstage: boolean): Promise<void> {
  return withPending(stageKey(project, path, unstage), async () => {
    try {
      await api.gitStage({ project, path, unstage });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
    } catch (err) {
      toastFailure(`${unstage ? "Unstage" : "Stage"} ${path} failed`, err);
    }
  }).then(() => undefined);
}

const commitKey = (project: string) => `git.commit|${project}`;

export const commitPending = (project: string) => isPending(commitKey(project));

/**
 * Commits the staged changes, or amends HEAD when `amend` is set (an empty
 * message then keeps HEAD's message). Resolves true on success.
 */
export async function commitStaged(project: string, message: string, amend = false): Promise<boolean> {
  const ok = await withPending(commitKey(project), async () => {
    try {
      await api.gitCommit({ project, message, amend });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      if (amend) toast.success("Amended HEAD", { description: message.split("\n")[0] || undefined });
      else toast.success("Committed", { description: message.split("\n")[0] });
      return true;
    } catch (err) {
      toastFailure(amend ? "Amend failed" : "Commit failed", err);
      return false;
    }
  });
  return ok ?? false;
}

const stashKey = (project: string, op: "pop" | "apply" | "drop", index: string) => `git.stash|${project}|${op}|${index}`;

export const stashPending = (project: string, op: "pop" | "apply" | "drop", index: string) =>
  isPending(stashKey(project, op, index));

const STASH_VERB = { pop: "Popped", apply: "Applied", drop: "Dropped" } as const;

/** Pops (restore and remove), applies (restore, keep) or drops a stash entry. */
export function stashEntry(project: string, op: "pop" | "apply" | "drop", index: string): Promise<void> {
  return withPending(stashKey(project, op, index), async () => {
    try {
      await api.gitStash({ project, op, index });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success(`${STASH_VERB[op]} ${index}`);
    } catch (err) {
      toastFailure(`${op[0]!.toUpperCase()}${op.slice(1)} ${index} failed`, err);
    }
  }).then(() => undefined);
}

const checkoutKey = (project: string, branch: string) => `git.checkout|${project}|${branch}`;

export const checkoutPending = (project: string, branch: string) => isPending(checkoutKey(project, branch));

/** Checks out a local branch, or creates a local branch from a remote one. */
export function checkoutBranch(project: string, branch: string): Promise<void> {
  return withPending(checkoutKey(project, branch), async () => {
    try {
      await api.gitCheckout({ project, branch });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success(`Checked out ${branch}`);
    } catch (err) {
      toastFailure(`Checkout ${branch} failed`, err);
    }
  }).then(() => undefined);
}

const branchKey = (project: string) => `git.branch|${project}`;

export const branchPending = (project: string) => isPending(branchKey(project));

/** Creates a branch (from `start`, or HEAD) and optionally checks it out. */
export function createBranch(project: string, name: string, start: string, checkout: boolean): Promise<boolean> {
  return withPending(branchKey(project), async () => {
    try {
      await api.gitBranchCreate({ project, name, start, checkout });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success(checkout ? `Created and checked out ${name}` : `Created ${name}`, {
        description: start ? `from ${start}` : undefined,
      });
      return true;
    } catch (err) {
      toastFailure(`Create branch ${name} failed`, err);
      return false;
    }
  }).then((ok) => ok ?? false);
}

const restoreKey = (project: string, path: string) => `git.restore|${project}|${path}`;

export const restorePending = (project: string, path: string) => isPending(restoreKey(project, path));

/** Discards a path's local changes (path null = every path). */
export function discardChanges(project: string, path: string | null): Promise<void> {
  return withPending(restoreKey(project, path ?? "*"), async () => {
    try {
      await api.gitRestore(path === null ? { project, all: true } : { project, path });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success(`Discarded changes in ${path ?? project}`);
    } catch (err) {
      toastFailure(`Discard ${path ?? "all changes"} failed`, err);
    }
  }).then(() => undefined);
}

/** The full message (subject and body) of a commit, for amending. */
export async function fetchCommitMessage(project: string, hash: string): Promise<string> {
  const r = (await api.gitDiff({ project, hash })).result?.commit;
  if (!r) return "";
  return r.subject + (r.body ? `\n\n${r.body}` : "");
}
