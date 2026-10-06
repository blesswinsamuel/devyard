import { describe, expect, it } from "vitest";
import { chunkHunks, hunkPatch, parseDiff, parseGitMeta } from "./diff";

const RAW = [
  "diff --git a/src/app.go b/src/app.go",
  "index 4a49a2d..e785d2a 100644",
  "--- a/src/app.go",
  "+++ b/src/app.go",
  "@@ -3,4 +3,5 @@ package main",
  " func f() int {",
  "-\treturn 1",
  "+\treturn 2",
  "+\t// +++ not a header",
  " }",
  "",
  "@@ -20,2 +21,2 @@ func g()",
  "-old",
  "+new",
  "\\ No newline at end of file",
  "diff --git a/bin/run b/bin/run",
  "old mode 100644",
  "new mode 100755",
  "diff --git a/my file.txt b/my file.txt",
  "new file mode 100644",
  "index 0000000..fa49b07",
  "--- /dev/null",
  "+++ b/my file.txt",
  "@@ -0,0 +1 @@",
  "+hello",
  "diff --git a/logo.png b/logo.png",
  "index 1111111..2222222 100644",
  "Binary files a/logo.png and b/logo.png differ",
  "",
].join("\n");

describe("parseDiff", () => {
  const chunks = parseDiff(RAW);

  it("splits the diff per file", () => {
    expect(chunks.map((c) => c.filePath)).toEqual(["src/app.go", "bin/run", "my file.txt", "logo.png"]);
    expect(chunks[0]!.header).toBe("diff --git a/src/app.go b/src/app.go");
  });

  it("numbers old and new lines from hunk headers", () => {
    const lines = chunks[0]!.lines;
    expect(lines.map((l) => l.type)).toEqual([
      "hunk",
      "context",
      "delete",
      "add",
      "add",
      "context",
      "context",
      "hunk",
      "delete",
      "add",
      "note",
    ]);
    expect(lines[1]).toMatchObject({ oldLine: 3, newLine: 3 });
    expect(lines[2]).toMatchObject({ type: "delete", oldLine: 4 });
    expect(lines[2]!.newLine).toBeUndefined();
    expect(lines[3]).toMatchObject({ type: "add", newLine: 4 });
    // Content that looks like a file header inside a hunk is still content.
    expect(lines[4]).toMatchObject({ type: "add", text: "+\t// +++ not a header", newLine: 5 });
    expect(lines[5]).toMatchObject({ oldLine: 5, newLine: 6 });
    // A blank context line (trailing whitespace stripped) keeps numbering.
    expect(lines[6]).toMatchObject({ type: "context", text: "", oldLine: 6, newLine: 7 });
    expect(lines[8]).toMatchObject({ type: "delete", oldLine: 20 });
    expect(lines[9]).toMatchObject({ type: "add", newLine: 21 });
    expect(lines[10]).toEqual({ type: "note", text: "\\ No newline at end of file" });
  });

  it("keeps file headers out of the body and in meta", () => {
    expect(chunks[0]!.metaLines).toEqual(["index 4a49a2d..e785d2a 100644"]);
    expect(chunks[1]!.lines).toEqual([]);
    expect(chunks[1]!.metaLines).toEqual(["old mode 100644", "new mode 100755"]);
  });

  it("shows binary markers as notes", () => {
    expect(chunks[3]!.lines).toEqual([{ type: "note", text: "Binary files a/logo.png and b/logo.png differ" }]);
  });

  it("returns nothing for an empty diff", () => {
    expect(parseDiff("")).toEqual([]);
  });

  it("uses the new path for renames", () => {
    const [chunk] = parseDiff("diff --git a/old.txt b/new.txt\nsimilarity index 100%\nrename from old.txt\nrename to new.txt\n");
    expect(chunk!.filePath).toBe("new.txt");
    expect(chunk!.metaLines).toContain("rename from old.txt");
  });

  it("keeps the raw prelude (meta and ---/+++) for patch rebuilding", () => {
    expect(chunks[0]!.prelude).toEqual(["index 4a49a2d..e785d2a 100644", "--- a/src/app.go", "+++ b/src/app.go"]);
  });
});

describe("hunk patches", () => {
  const [chunk] = parseDiff(RAW);

  it("groups lines into hunks", () => {
    const hunks = chunkHunks(chunk!);
    expect(hunks).toHaveLength(2);
    expect(hunks[0]!.header.text).toBe("@@ -3,4 +3,5 @@ package main");
    expect(hunks[1]!.body.map((l) => l.type)).toEqual(["delete", "add", "note"]);
  });

  it("rebuilds a single-hunk patch that git can apply", () => {
    // Context lines in real diffs start with a space (the fixture above uses
    // an empty line for a blank context), so use a realistic hunk here.
    const [c] = parseDiff(
      [
        "diff --git a/a.txt b/a.txt",
        "index 1234567..89abcde 100644",
        "--- a/a.txt",
        "+++ b/a.txt",
        "@@ -1,2 +1,3 @@",
        " one",
        "+two",
        " three",
        "@@ -10,2 +11,2 @@",
        "-old",
        "+new",
        "",
      ].join("\n"),
    );
    expect(hunkPatch(c!, chunkHunks(c!)[0]!)).toBe(
      [
        "diff --git a/a.txt b/a.txt",
        "index 1234567..89abcde 100644",
        "--- a/a.txt",
        "+++ b/a.txt",
        "@@ -1,2 +1,3 @@",
        " one",
        "+two",
        " three",
      ].join("\n") + "\n",
    );
  });
});

describe("parseGitMeta", () => {
  it("reads blob hashes and file mode", () => {
    expect(parseGitMeta(["index 4a49a2d..e785d2a 100755"])).toEqual({
      blobs: { oldHash: "4a49a2d", newHash: "e785d2a" },
      fileMode: "755 (executable)",
      isExecutable: true,
    });
  });

  it("detects mode changes, new and deleted files", () => {
    expect(parseGitMeta(["old mode 100644", "new mode 100755"]).modeChange).toEqual({
      oldMode: "100644",
      newMode: "755 (executable)",
    });
    expect(parseGitMeta(["new file mode 100644", "index 0000000..fa49b07"])).toMatchObject({ isNew: true, fileMode: "100644" });
    expect(parseGitMeta(["deleted file mode 100644"]).isDeleted).toBe(true);
  });
});
