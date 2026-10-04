import { describe, expect, it, vi } from "vitest";
import {
  ACTIONS,
  actionForShortcut,
  actionsFor,
  getAction,
  resolveContext,
  type ActionEnv,
} from "./actions";
import { createEntityStore } from "./entities";
import { git, project, service, snapshot, task } from "~/test/fixtures";

function stateWith(...args: Parameters<typeof snapshot>[1][]) {
  const store = createEntityStore();
  store.apply(snapshot(1, Object.assign({}, ...args)));
  return store.state;
}

const ids = (list: { id: string }[]) => list.map((a) => a.id);

describe("action registry", () => {
  it("has unique ids", () => {
    expect(new Set(ids(ACTIONS)).size).toBe(ACTIONS.length);
  });

  it("offers move up/down by position in the project list", () => {
    const state = stateWith({
      projects: [project({ id: "b", position: 1 }), project({ id: "a", position: 0 }), project({ id: "c", position: 2 })],
    });
    const moves = (id: string) =>
      ids(actionsFor(resolveContext({ kind: "project", project: id }, state))).filter((a) => a.startsWith("project.move"));
    expect(moves("a")).toEqual(["project.move-down"]);
    expect(moves("b")).toEqual(["project.move-up", "project.move-down"]);
    expect(moves("c")).toEqual(["project.move-up"]);
  });

  it("moves a project one place through the API", async () => {
    const state = stateWith({
      projects: [project({ id: "a", position: 0 }), project({ id: "b", position: 1 }), project({ id: "c", position: 2 })],
    });
    const moveProject = vi.fn().mockResolvedValue({});
    const env = { api: { moveProject } } as unknown as ActionEnv;
    const ctx = (id: string) => resolveContext({ kind: "project", project: id }, state);
    await getAction("project.move-down")!.run(ctx("a"), env);
    await getAction("project.move-up")!.run(ctx("c"), env);
    expect(moveProject.mock.calls).toEqual([[{ project: "a", index: 1 }], [{ project: "c", index: 1 }]]);
  });

  it("offers stop/restart/kill for a running service, start for a stopped one", () => {
    const running = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service()] }),
    );
    expect(ids(actionsFor(running))).toEqual([
      "service.stop",
      "service.restart",
      "service.kill",
      "service.logs",
      "service.pin-logs",
    ]);
    const stopped = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service({ status: "exited", exitCode: 1 })] }),
    );
    expect(ids(actionsFor(stopped))).toEqual(["service.start", "service.logs", "service.pin-logs"]);
  });

  it("gates attach, rebuild and open-url on the spec", () => {
    const state = stateWith({
      projects: [project()],
      services: [
        service({
          urls: ["http://api.web.localhost"],
          spec: { command: "x", tty: true, buildCommand: "make" },
        }),
      ],
    });
    const ctx = resolveContext({ kind: "service", project: "web", name: "api" }, state);
    const got = ids(actionsFor(ctx));
    expect(got).toContain("service.attach");
    expect(got).toContain("service.rebuild");
    expect(got).toContain("service.open-url");
  });

  it("handles task lifecycle", () => {
    const idle = resolveContext({ kind: "task", project: "web", name: "migrate" }, stateWith({ tasks: [task()] }));
    expect(ids(actionsFor(idle))).toEqual(["task.run", "task.run-args", "task.open"]);
    const running = resolveContext(
      { kind: "task", project: "web", name: "migrate" },
      stateWith({ tasks: [task({ status: "running" })] }),
    );
    expect(ids(actionsFor(running))).toEqual(["task.stop", "task.kill", "task.open", "task.attach"]);
  });

  it("offers start for a stopped project and hides lifecycle actions on config errors", () => {
    const stopped = resolveContext({ kind: "project", project: "web" }, stateWith({ projects: [project({ status: "stopped", desired: "stopped" })] }));
    expect(ids(actionsFor(stopped))).toEqual([
      "project.start",
      "project.reload",
      "project.rebuild",
      "project.terminal",
      "project.git",
      "project.open",
      "project.pin-logs",
      "project.remove",
    ]);
    const broken = resolveContext(
      { kind: "project", project: "web" },
      stateWith({ projects: [project({ status: "error", error: "devyard.yml: no such file" })] }),
    );
    expect(ids(actionsFor(broken))).toEqual(["project.reload", "project.git", "project.open", "project.pin-logs", "project.remove"]);
  });

  it("hides git for non-repos and inherits terminal/git only when asked", () => {
    const state = stateWith({ projects: [project()], services: [service()], git: [git({ isRepo: false })] });
    const ctx = resolveContext({ kind: "service", project: "web", name: "api" }, state);
    expect(ids(actionsFor(ctx))).not.toContain("project.terminal");
    const inherited = ids(actionsFor(ctx, { inherit: true }));
    expect(inherited).toContain("project.terminal");
    expect(inherited).not.toContain("project.git");
    expect(inherited).not.toContain("project.stop");
  });

  it("offers git actions for idle repos and hides remote ones while a sync runs", () => {
    const gitIds = (g: Parameters<typeof git>[0]) =>
      ids(actionsFor(resolveContext({ kind: "project", project: "web" }, stateWith({ projects: [project()], git: [git(g)] })))).filter((id) =>
        id.startsWith("git."),
      );
    expect(gitIds({})).toEqual(["git.fetch", "git.pull", "git.push"]);
    expect(gitIds({ dirty: 2, staged: 1 })).toEqual(["git.fetch", "git.pull", "git.push", "git.stage-all", "git.unstage-all", "git.commit"]);
    expect(gitIds({ untracked: 1, syncOperation: "pull" })).toEqual(["git.stage-all"]);
    expect(gitIds({ isRepo: false, dirty: 1 })).toEqual([]);
    // Not inherited: a service page doesn't offer the project's git operations.
    const svcCtx = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service()], git: [git()] }),
    );
    expect(ids(actionsFor(svcCtx, { inherit: true })).some((id) => id.startsWith("git."))).toBe(false);
  });

  it("git actions call the RPCs and report their output", async () => {
    const api = {
      gitFetch: vi.fn().mockResolvedValue({ output: "From origin\n * branch main\n" }),
      gitPush: vi.fn().mockResolvedValue({ output: "" }),
      gitStage: vi.fn().mockResolvedValue({}),
    };
    const env = { api, notify: vi.fn(), openGitCommit: vi.fn() } as unknown as ActionEnv;
    const ctx = resolveContext({ kind: "project", project: "web" }, stateWith({ projects: [project()], git: [git({ staged: 1, dirty: 1 })] }));
    await getAction("git.fetch").run(ctx, env);
    expect(api.gitFetch).toHaveBeenCalledWith({ project: "web" });
    expect(env.notify).toHaveBeenCalledWith("Fetched web", "From origin\n * branch main");
    await getAction("git.push").run(ctx, env);
    expect(env.notify).toHaveBeenLastCalledWith("Pushed web", undefined);
    await getAction("git.unstage-all").run(ctx, env);
    expect(api.gitStage).toHaveBeenCalledWith({ project: "web", stageAll: true, unstage: true });
    await getAction("git.commit").run(ctx, env);
    expect(env.openGitCommit).toHaveBeenCalledWith("web");
  });

  it("resolves shortcuts by state: s means stop when running, start when stopped", () => {
    const is = (k: string) => (s: string) => s === k;
    const running = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service()] }),
    );
    expect(actionForShortcut(running, is("s"))?.id).toBe("service.stop");
    expect(actionForShortcut(running, is("t"))?.id).toBe("project.terminal");
    expect(actionForShortcut(running, is("?"))?.id).toBe("app.shortcuts");
    const stopped = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service({ status: "stopped" })] }),
    );
    expect(actionForShortcut(stopped, is("s"))?.id).toBe("service.start");
    // Kill isn't available when stopped, and must not fall through to anything else.
    expect(actionForShortcut(stopped, is("k"))).toBeUndefined();
    // Never escalate a service key to the whole project.
    const stopping = resolveContext(
      { kind: "service", project: "web", name: "api" },
      stateWith({ projects: [project()], services: [service({ status: "stopping" })] }),
    );
    expect(actionForShortcut(stopping, is("s"))).toBeUndefined();
  });

  it("app actions depend on the daemon", () => {
    const ctx = resolveContext({ kind: "app" }, stateWith({}));
    expect(ids(actionsFor(ctx, { includeApp: true }))).toContain("daemon.stop");
    const draining = resolveContext({ kind: "app" }, { ...stateWith({}), daemon: { ...stateWith({}).daemon!, draining: true } });
    expect(ids(actionsFor(draining, { includeApp: true }))).not.toContain("daemon.stop");
  });

  it("destructive actions require confirmation", () => {
    for (const a of ACTIONS.filter((x) => x.destructive)) expect(a.confirm, a.id).toBeDefined();
  });

  it("run calls the right RPC", async () => {
    const api = { restartService: vi.fn().mockResolvedValue({}), runTask: vi.fn().mockResolvedValue({ run: 2n }) };
    const env = { api, promptTaskArgs: vi.fn().mockResolvedValue(["--dry-run"]) } as unknown as ActionEnv;
    const state = stateWith({ services: [service()], tasks: [task()] });
    await getAction("service.restart").run(resolveContext({ kind: "service", project: "web", name: "api" }, state), env);
    expect(api.restartService).toHaveBeenCalledWith({ project: "web", service: "api" });
    await getAction("task.run-args").run(resolveContext({ kind: "task", project: "web", name: "migrate" }, state), env);
    expect(api.runTask).toHaveBeenCalledWith({ project: "web", task: "migrate", args: ["--dry-run"] });
  });
});
