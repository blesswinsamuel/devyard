import { describe, expect, it } from "vitest";
import { toGit, toProject, toService, toTask } from "~/data/entities";
import { git, project, service, task } from "~/test/fixtures";
import { buildTree, dropIndex, gitSummary, treeKey, type TreeInput } from "./tree";

const input = (over: Partial<TreeInput> = {}): TreeInput => ({
  projects: [toProject(project({ id: "blog" })), toProject(project({ id: "shop" }))],
  servicesOf: (p) => (p === "shop" ? [toService(service({ project: "shop", name: "api" })), toService(service({ project: "shop", name: "db" }))] : []),
  tasksOf: (p) => (p === "shop" ? [toTask(task({ project: "shop", name: "migrate" }))] : []),
  expanded: () => true,
  filter: "",
  ...over,
});

const ids = (nodes: { id: string }[]) => nodes.map((n) => n.id);

describe("buildTree", () => {
  it("flattens projects with services then tasks", () => {
    expect(ids(buildTree(input()))).toEqual([
      "project:blog",
      "project:shop",
      "service:shop/api",
      "service:shop/db",
      "task:shop/migrate",
    ]);
  });

  it("hides children of collapsed projects", () => {
    expect(ids(buildTree(input({ expanded: (p) => p !== "shop" })))).toEqual(["project:blog", "project:shop"]);
  });

  it("filters children and force-expands their project", () => {
    const nodes = buildTree(input({ filter: "DB", expanded: () => false }));
    expect(ids(nodes)).toEqual(["project:shop", "service:shop/db"]);
    expect(nodes[0]!.expanded).toBe(true);
    expect(nodes[1]).toMatchObject({ posinset: 1, setsize: 1, parentId: "project:shop" });
  });

  it("shows every child when the project name matches", () => {
    expect(ids(buildTree(input({ filter: "sho" })))).toHaveLength(4);
  });
});

describe("treeKey", () => {
  const nodes = buildTree(input());
  it("moves with arrows, Home and End", () => {
    expect(treeKey(nodes, "project:blog", "ArrowDown")?.focus).toBe("project:shop");
    expect(treeKey(nodes, "project:blog", "ArrowUp")?.focus).toBe("project:blog");
    expect(treeKey(nodes, "service:shop/api", "End")?.focus).toBe("task:shop/migrate");
    expect(treeKey(nodes, "task:shop/migrate", "Home")?.focus).toBe("project:blog");
  });
  it("expands, enters, collapses and returns to the parent", () => {
    expect(treeKey(nodes, "project:shop", "ArrowRight")?.focus).toBe("service:shop/api");
    expect(treeKey(nodes, "project:shop", "ArrowLeft")?.toggle).toEqual({ project: "shop", expanded: false });
    expect(treeKey(nodes, "service:shop/db", "ArrowLeft")?.focus).toBe("project:shop");
    const collapsed = buildTree(input({ expanded: () => false }));
    expect(treeKey(collapsed, "project:shop", "ArrowRight")?.toggle).toEqual({ project: "shop", expanded: true });
  });
  it("activates on Enter and ignores other keys", () => {
    expect(treeKey(nodes, "service:shop/api", "Enter")?.activate?.name).toBe("api");
    expect(treeKey(nodes, "service:shop/api", "x")).toBeNull();
  });
});

describe("gitSummary", () => {
  it("shows branch with ahead/behind", () => {
    expect(gitSummary(toGit(git({ ahead: 2, behind: 1 })))).toBe("main ↑2 ↓1");
    expect(gitSummary(toGit(git({ isRepo: false })))).toBe("");
  });
});

describe("dropIndex", () => {
  const order = ["a", "b", "c", "d"];
  it("computes the index in the list without the dragged project", () => {
    expect(dropIndex(order, "a", "c", false)).toBe(1); // b a c d
    expect(dropIndex(order, "a", "c", true)).toBe(2); // b c a d
    expect(dropIndex(order, "d", "a", false)).toBe(0);
    expect(dropIndex(order, "d", "b", true)).toBe(2);
    expect(dropIndex(order, "b", "d", true)).toBe(3);
  });
  it("ignores drops that change nothing", () => {
    expect(dropIndex(order, "b", "b", true)).toBeNull();
    expect(dropIndex(order, "b", "a", true)).toBeNull(); // already right after a
    expect(dropIndex(order, "b", "c", false)).toBeNull(); // already right before c
    expect(dropIndex(order, "x", "a", false)).toBeNull();
    expect(dropIndex(order, "a", "x", false)).toBeNull();
  });
});
