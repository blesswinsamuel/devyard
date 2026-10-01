import { describe, expect, it } from "vitest";
import { createEntityStore, entityKey, type EntityEvent } from "./entities";
import { change, git, heartbeat, project, removed, service, snapshot, task } from "~/test/fixtures";

describe("entity store", () => {
  it("applies a snapshot as the full state with plain values", () => {
    const store = createEntityStore();
    store.apply(
      snapshot(5, {
        projects: [project()],
        services: [service({ startedAtUnixMs: 1700000000000n, urls: ["http://api.web.localhost"] })],
        tasks: [task()],
        git: [git()],
      }),
    );
    const s = store.state;
    expect(s.loaded).toBe(true);
    expect(s.revision).toBe(5);
    expect(s.daemon?.pid).toBe(42);
    expect(s.projects.web?.status).toBe("running");
    const api = s.services[entityKey("web", "api")]!;
    expect(api.startedAt).toBe(1700000000000);
    expect(typeof api.run).toBe("number");
    expect(api.spec.command).toBe("go run .");
    expect(api.spec.ready).toBeNull();
    expect(s.tasks["web/migrate"]?.spec.tty).toBe(true);
    expect(s.git.web?.branch).toBe("main");
  });

  it("upserts changes and emits transitions with the previous value", () => {
    const store = createEntityStore();
    const events: EntityEvent[] = [];
    store.subscribe((e) => events.push(e));
    store.apply(snapshot(1, { projects: [project()], services: [service()] }));
    store.apply(change(2, { case: "service", value: service({ status: "exited", exitCode: 1 }) }));
    store.apply(change(3, { case: "service", value: service({ name: "db", status: "starting" }) }));

    expect(store.state.revision).toBe(3);
    expect(store.state.services["web/api"]?.status).toBe("exited");
    expect(store.state.services["web/db"]?.status).toBe("starting");
    const svcEvents = events.filter((e) => e.kind === "service");
    expect(svcEvents).toHaveLength(2);
    expect(svcEvents[0]).toMatchObject({ prev: { status: "running" }, next: { status: "exited", exitCode: 1 } });
    expect(svcEvents[1]).toMatchObject({ prev: undefined, next: { name: "db" } });
  });

  it("keeps store object identity on reconcile (fine-grained updates)", () => {
    const store = createEntityStore();
    store.apply(snapshot(1, { services: [service()] }));
    const before = store.state.services["web/api"];
    store.apply(change(2, { case: "service", value: service({ restarts: 3 }) }));
    expect(store.state.services["web/api"]).toBe(before);
    expect(before?.restarts).toBe(3);
  });

  it("ignores changes at or below the applied revision", () => {
    const store = createEntityStore();
    store.apply(snapshot(10, { services: [service({ status: "running" })] }));
    store.apply(change(9, { case: "service", value: service({ status: "failed" }) }));
    store.apply(change(10, { case: "service", value: service({ status: "failed" }) }));
    expect(store.state.services["web/api"]?.status).toBe("running");
  });

  it("removes entities, cascading project removal to its children", () => {
    const store = createEntityStore();
    store.apply(
      snapshot(1, {
        projects: [project(), project({ id: "other" })],
        services: [service(), service({ name: "db" }), service({ project: "other", name: "x" })],
        tasks: [task()],
        git: [git()],
      }),
    );
    store.apply(removed(2, "service", "web", "db"));
    expect(store.state.services["web/db"]).toBeUndefined();
    store.apply(removed(3, "project", "web"));
    expect(Object.keys(store.state.projects)).toEqual(["other"]);
    expect(Object.keys(store.state.services)).toEqual(["other/x"]);
    expect(store.state.tasks).toEqual({});
    expect(store.state.git).toEqual({});
  });

  it("treats a later snapshot as full replacement (resync)", () => {
    const store = createEntityStore();
    const events: EntityEvent[] = [];
    store.subscribe((e) => events.push(e));
    store.apply(snapshot(7, { projects: [project()], services: [service(), service({ name: "db" })] }));
    // Daemon restarted: revisions restart and state differs.
    store.apply(snapshot(1, { projects: [project({ status: "stopped" })], services: [service({ status: "stopped" })] }));
    expect(store.state.revision).toBe(1);
    expect(Object.keys(store.state.services)).toEqual(["web/api"]);
    expect(store.state.services["web/api"]?.status).toBe("stopped");
    expect(store.state.projects.web?.status).toBe("stopped");
    // A change after the resync applies even though its revision < the old one.
    store.apply(change(2, { case: "service", value: service({ status: "starting" }) }));
    expect(store.state.services["web/api"]?.status).toBe("starting");
    // Snapshots never produce transition events (no crash toasts on load).
    expect(events.filter((e) => e.kind === "service")).toHaveLength(1);
  });

  it("ignores heartbeats", () => {
    const store = createEntityStore();
    store.apply(snapshot(3, { projects: [project()] }));
    store.apply(heartbeat(99));
    expect(store.state.revision).toBe(3);
  });

  it("updates daemon info and git status", () => {
    const store = createEntityStore();
    store.apply(snapshot(1, { git: [git()] }));
    store.apply(change(2, { case: "git", value: git({ ahead: 2, changeSeq: 5n }) }));
    expect(store.state.git.web).toMatchObject({ ahead: 2, changeSeq: 5 });
  });
});
