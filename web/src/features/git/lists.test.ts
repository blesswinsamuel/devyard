import { describe, expect, it } from "vitest";
import { GitCommit, GitDiffResult, GitFileChange, GitLogResponse, GitStash } from "~/gen/devyard/v1/control_pb";
import { filterCommits } from "./commit-list";
import { fileOrder, groupFiles } from "./file-list";
import { toDiff, toLog, type GitFileView } from "./git-data";

const file = (path: string, f: Partial<GitFileView> = {}): GitFileView => ({
  path,
  oldPath: "",
  status: "M",
  additions: 0,
  deletions: 0,
  staged: false,
  unstaged: false,
  untracked: false,
  ...f,
});

describe("groupFiles / fileOrder", () => {
  const files = [
    file("a.go", { staged: true }),
    file("new.txt", { status: "A", untracked: true }),
    file("b.go", { unstaged: true }),
    file("both.go", { staged: true, unstaged: true }),
  ];

  it("splits the working tree into staged and changes (untracked last)", () => {
    const g = groupFiles(files, "");
    expect(g.staged.map((f) => f.path)).toEqual(["a.go", "both.go"]);
    expect(g.changes.map((f) => f.path)).toEqual(["b.go", "both.go", "new.txt"]);
  });

  it("filters by path and orders navigation without duplicates", () => {
    expect(groupFiles(files, "GO").all.map((f) => f.path)).toEqual(["a.go", "b.go", "both.go"]);
    expect(fileOrder(groupFiles(files, ""), true)).toEqual([null, "a.go", "both.go", "b.go", "new.txt"]);
    expect(fileOrder(groupFiles(files, ""), false)).toEqual([null, "a.go", "new.txt", "b.go", "both.go"]);
  });
});

describe("filterCommits", () => {
  const log = toLog(
    new GitLogResponse({
      commits: [
        new GitCommit({ hash: "abc123", subject: "Fix crash", author: "Ada" }),
        new GitCommit({ hash: "def456", subject: "Add search", author: "Grace" }),
      ],
    }),
  );

  it("matches subject, author or hash prefix, case-insensitively", () => {
    expect(filterCommits(log.commits, "").length).toBe(2);
    expect(filterCommits(log.commits, "crash").map((c) => c.hash)).toEqual(["abc123"]);
    expect(filterCommits(log.commits, "grace").map((c) => c.hash)).toEqual(["def456"]);
    expect(filterCommits(log.commits, "DEF4").map((c) => c.hash)).toEqual(["def456"]);
    expect(filterCommits(log.commits, "123")).toEqual([]);
  });
});

describe("proto mapping", () => {
  it("converts bigint times and copies nested messages", () => {
    const log = toLog(
      new GitLogResponse({
        commits: [
          new GitCommit({
            hash: "h",
            timeUnixMs: 1_700_000_000_000n,
            parents: ["p"],
            refs: [{ name: "main", type: "branch", isActive: true }],
          }),
        ],
        stashes: [new GitStash({ index: "stash@{0}", name: "wip", hash: "s", timeUnixMs: 5n })],
      }),
    );
    expect(log.commits[0]).toMatchObject({
      hash: "h",
      time: 1_700_000_000_000,
      parents: ["p"],
      refs: [{ name: "main", type: "branch", isActive: true }],
    });
    expect(log.stashes[0]).toEqual({ index: "stash@{0}", name: "wip", hash: "s", time: 5 });
  });

  it("tolerates an empty diff result", () => {
    expect(toDiff("WORKDIR", 3, undefined)).toEqual({ hash: "WORKDIR", contextLines: 3, commit: null, files: [], diff: "" });
    const d = toDiff("h", 10, new GitDiffResult({ files: [new GitFileChange({ path: "x", untracked: true })], diff: "d" }));
    expect(d.files[0]).toMatchObject({ path: "x", untracked: true });
  });
});
