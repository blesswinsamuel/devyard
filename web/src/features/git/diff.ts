// Unified-diff parsing for the commit / working-tree diff viewer.

export interface ParsedDiffLine {
  type: "add" | "delete" | "context" | "hunk" | "note";
  text: string;
  oldLine?: number;
  newLine?: number;
}

export interface ParsedFileChunk {
  header: string;
  filePath: string;
  metaLines: string[];
  /** Raw lines before the first hunk (meta lines plus `---`/`+++`). */
  prelude: string[];
  lines: ParsedDiffLine[];
}

export interface ParsedGitMeta {
  blobs?: { oldHash: string; newHash: string };
  fileMode?: string;
  isExecutable?: boolean;
  isNew?: boolean;
  isDeleted?: boolean;
  modeChange?: { oldMode: string; newMode: string };
}

function modeLabel(modeCode: string): string {
  return modeCode === "100755" ? "755 (executable)" : modeCode;
}

/** Extracts display metadata (blob hashes, modes) from a chunk's meta lines. */
export function parseGitMeta(metaLines: readonly string[]): ParsedGitMeta {
  const meta: ParsedGitMeta = {};
  let oldMode = "";
  let newMode = "";

  for (const line of metaLines) {
    const index = line.match(/^index ([0-9a-fA-F]+)\.\.([0-9a-fA-F]+)(?: (\d+))?/);
    if (index) {
      meta.blobs = { oldHash: index[1]!, newHash: index[2]! };
      if (index[3]) {
        meta.fileMode = modeLabel(index[3]);
        meta.isExecutable = index[3] === "100755";
      }
    }
    if (line.startsWith("new file mode ")) {
      meta.isNew = true;
      const m = line.slice("new file mode ".length).trim();
      meta.fileMode = modeLabel(m);
      if (m === "100755") meta.isExecutable = true;
    }
    if (line.startsWith("deleted file mode ")) meta.isDeleted = true;
    if (line.startsWith("old mode ")) oldMode = line.slice("old mode ".length).trim();
    if (line.startsWith("new mode ")) newMode = line.slice("new mode ".length).trim();
  }

  if (oldMode && newMode) meta.modeChange = { oldMode: modeLabel(oldMode), newMode: modeLabel(newMode) };
  return meta;
}

/** Splits a raw unified diff into per-file chunks with numbered lines. */
export function parseDiff(raw: string): ParsedFileChunk[] {
  if (!raw) return [];
  const chunks: ParsedFileChunk[] = [];

  for (const part of raw.split(/^diff --git /m)) {
    if (!part.trim()) continue;
    const rawLines = part.split("\n");
    const headerLine = rawLines[0]!;
    const paths = headerLine.match(/^a\/(.+?)\s+b\/(.+)$/);
    const filePath = paths ? paths[2]! : (headerLine.match(/b\/(.+)$/)?.[1] ?? headerLine);

    const metaLines: string[] = [];
    const prelude: string[] = [];
    const lines: ParsedDiffLine[] = [];
    let oldLine = 1;
    let newLine = 1;
    let inHunk = false;

    for (let i = 1; i < rawLines.length; i++) {
      const line = rawLines[i]!;
      // Trailing empty element from the final newline: not a diff line.
      if (i === rawLines.length - 1 && line === "") break;
      if (line.startsWith("@@")) {
        const hunk = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
        if (hunk) {
          oldLine = parseInt(hunk[1]!, 10);
          newLine = parseInt(hunk[2]!, 10);
        }
        inHunk = true;
        lines.push({ type: "hunk", text: line });
      } else if (!inHunk) {
        // File header: "--- a/x" / "+++ b/x" are implied by the chunk; binary
        // markers are shown in the body so the file doesn't look empty.
        prelude.push(line);
        if (line.startsWith("Binary files ")) lines.push({ type: "note", text: line });
        else if (!line.startsWith("--- ") && !line.startsWith("+++ ")) metaLines.push(line);
      } else if (line.startsWith("+")) {
        lines.push({ type: "add", text: line, newLine: newLine++ });
      } else if (line.startsWith("-")) {
        lines.push({ type: "delete", text: line, oldLine: oldLine++ });
      } else if (line.startsWith("\\")) {
        // "\ No newline at end of file".
        lines.push({ type: "note", text: line.trim() });
      } else {
        lines.push({ type: "context", text: line, oldLine: oldLine++, newLine: newLine++ });
      }
    }

    chunks.push({ header: `diff --git ${headerLine}`, filePath, metaLines, prelude, lines });
  }
  return chunks;
}

export interface DiffHunk {
  header: ParsedDiffLine;
  body: ParsedDiffLine[];
}

/** Groups a chunk's lines into hunks: each `@@` header plus its body. */
export function chunkHunks(chunk: ParsedFileChunk): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  for (const line of chunk.lines) {
    if (line.type === "hunk") hunks.push({ header: line, body: [] });
    else if (hunks.length) hunks[hunks.length - 1]!.body.push(line);
  }
  return hunks;
}

/**
 * Builds a unified patch containing one hunk of a chunk, suitable for
 * `git apply` (staging, unstaging or discarding that hunk).
 */
export function hunkPatch(chunk: ParsedFileChunk, hunk: DiffHunk): string {
  return [chunk.header, ...chunk.prelude, hunk.header.text, ...hunk.body.map((l) => l.text)].join("\n") + "\n";
}
