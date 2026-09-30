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
  });
});
