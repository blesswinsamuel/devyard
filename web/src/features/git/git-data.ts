import { useQuery } from "@tanstack/solid-query";
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

export interface GitDiffView {
  hash: string;
  contextLines: number;
  commit: GitCommitView | null;
  files: GitFileView[];
  diff: string;
}

// `body` is part of GitCommit but not filled by the daemon yet; not mapped.
export const toCommit = (c: GitCommit): GitCommitView => ({
  hash: c.hash,
  short: c.short,
  author: c.author,
  email: c.email,
  time: Number(c.timeUnixMs),
  parents: [...c.parents],
  subject: c.subject,
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

export function toLog(res: GitLogResponse): GitLogView {
  return {
    commits: res.commits.map(toCommit),
    branches: res.branches.map(toBranch),
    tags: res.tags.map((t) => ({ name: t.name, hash: t.hash })),
    stashes: res.stashes.map(toStash),
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

/** Commits, branches, tags and stashes; refetched whenever change_seq moves. */
export function useGitLog(project: () => string) {
  return useQuery(() => ({
    queryKey: queryKeys.gitLog(project()),
    queryFn: async ({ signal }) => toLog(await api.gitLog({ project: project() }, { signal })),
  }));
}

/**
 * The diff of one commit, or of the working tree for WORKDIR. While context
 * lines change, the previous diff of the same commit stays on screen.
 */
export function useGitDiff(project: () => string, hash: () => string | undefined, contextLines: () => number) {
  return useQuery(() => {
    const h = hash() ?? "";
    const ctx = contextLines();
    const workdir = h === WORKDIR;
    return {
      queryKey: workdir ? queryKeys.gitWorkdirDiff(project(), ctx) : queryKeys.gitCommitDiff(project(), h, ctx),
      queryFn: async ({ signal }: { signal: AbortSignal }) =>
        toDiff(h, ctx, (await api.gitDiff({ project: project(), hash: h, contextLines: ctx }, { signal })).result),
      enabled: !!h,
      staleTime: workdir ? 0 : Infinity,
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

/** Commits the staged changes; resolves true on success. */
export async function commitStaged(project: string, message: string): Promise<boolean> {
  const ok = await withPending(commitKey(project), async () => {
    try {
      await api.gitCommit({ project, message });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success("Committed", { description: message.split("\n")[0] });
      return true;
    } catch (err) {
      toastFailure("Commit failed", err);
      return false;
    }
  });
  return ok ?? false;
}

const stashKey = (project: string, op: "pop" | "drop", index: string) => `git.stash|${project}|${op}|${index}`;

export const stashPending = (project: string, op: "pop" | "drop", index: string) =>
  isPending(stashKey(project, op, index));

/** Pops (restores and removes) or drops a stash entry. */
export function stashEntry(project: string, op: "pop" | "drop", index: string): Promise<void> {
  return withPending(stashKey(project, op, index), async () => {
    try {
      await api.gitStash({ project, op, index });
      await queryClient.invalidateQueries({ queryKey: queryKeys.git(project) });
      toast.success(op === "pop" ? `Popped ${index}` : `Dropped ${index}`);
    } catch (err) {
      toastFailure(`${op === "pop" ? "Pop" : "Drop"} ${index} failed`, err);
    }
  }).then(() => undefined);
}
