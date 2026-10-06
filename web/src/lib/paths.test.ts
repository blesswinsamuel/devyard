import { describe, expect, it } from "vitest";
import { paths, targetFromPath } from "./paths";

describe("targetFromPath", () => {
  it("maps routes to targets", () => {
    expect(targetFromPath("/")).toEqual({ kind: "app" });
    expect(targetFromPath("/settings")).toEqual({ kind: "app" });
    expect(targetFromPath("/projects/web")).toEqual({ kind: "project", project: "web" });
    expect(targetFromPath("/projects/web/git")).toEqual({ kind: "project", project: "web" });
    expect(targetFromPath(paths.gitCommit("web", "abc123"))).toEqual({ kind: "project", project: "web" });
    expect(targetFromPath(paths.service("web", "api db"))).toEqual({ kind: "service", project: "web", name: "api db" });
    expect(targetFromPath(paths.task("web", "migrate"))).toEqual({ kind: "task", project: "web", name: "migrate" });
  });

  it("builds git deep links", () => {
    expect(paths.git("my app")).toBe("/projects/my%20app/git");
    expect(paths.gitCommit("web", "WORKDIR")).toBe("/projects/web/git/commits/WORKDIR");
    expect(paths.gitHistory("web", "src/a.ts")).toBe("/projects/web/git?path=src%2Fa.ts");
    expect(paths.gitCommit("web", "abc", { path: "src/a.ts" })).toBe("/projects/web/git/commits/abc?path=src%2Fa.ts");
    expect(paths.gitCommit("web", "abc123", { base: "main" })).toBe("/projects/web/git/commits/abc123?base=main");
    expect(paths.gitCommit("web", "abc123", { path: "a", base: "main" })).toBe(
      "/projects/web/git/commits/abc123?path=a&base=main",
    );
  });
});
