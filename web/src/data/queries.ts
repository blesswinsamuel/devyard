import { QueryClient, useQuery } from "@tanstack/solid-query";
import { api } from "./client";
import { entities } from "./entities";

/** Request/response data that isn't part of the Watch state. */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 },
  },
});

export const queryKeys = {
  daemon: ["daemon"] as const,
  globalConfig: ["global-config"] as const,
  ports: (project: string) => ["ports", project] as const,
  /** Prefix for every git query of a project; invalidated on change_seq. */
  git: (project: string) => ["git", project] as const,
  gitLog: (project: string) => ["git", project, "log"] as const,
  gitWorkdirDiff: (project: string, contextLines: number) => ["git", project, "workdir", contextLines] as const,
  /** Commits are immutable, so their diffs live outside the invalidated prefix. */
  gitCommitDiff: (project: string, hash: string, contextLines: number) =>
    ["git-commit", project, hash, contextLines] as const,
};

export function useDaemonInfo(opts: { refetchIntervalMs?: number } = {}) {
  return useQuery(() => ({
    queryKey: queryKeys.daemon,
    queryFn: async () => (await api.getDaemon({})).info ?? null,
    refetchInterval: opts.refetchIntervalMs,
  }));
}

export function useGlobalConfig() {
  return useQuery(() => ({
    queryKey: queryKeys.globalConfig,
    queryFn: () => api.getGlobalConfig({}),
    staleTime: Infinity,
  }));
}

export function usePorts(project: () => string) {
  return useQuery(() => ({
    queryKey: queryKeys.ports(project()),
    queryFn: async () => (await api.listPorts({ project: project() })).ports,
    refetchInterval: 10_000,
  }));
}

// Any on-disk repo change bumps change_seq: drop cached logs/diffs.
entities.subscribe((e) => {
  if (e.kind === "git" && e.prev?.changeSeq !== e.next.changeSeq)
    void queryClient.invalidateQueries({ queryKey: queryKeys.git(e.next.project) });
});
