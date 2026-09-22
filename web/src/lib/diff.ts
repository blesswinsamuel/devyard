// Unified-diff parsing shared by the commit diff viewer.

export interface ParsedDiffLine {
  type: "header" | "add" | "delete" | "context" | "hunk" | "note";
  text: string;
  oldLine?: number;
  newLine?: number;
}

export interface ParsedFileChunk {
  header: string;
  filePath: string;
  metaLines: string[];
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
  return modeCode === "100755" ? "755 (Executable)" : modeCode;
}

/** Extracts display metadata (blob hashes, modes) from a chunk's meta lines. */
export function parseGitMeta(metaLines: string[]): ParsedGitMeta {
  const meta: ParsedGitMeta = {};
  let oldMode = "";
  let newMode = "";

  for (const line of metaLines) {
    const indexMatch = line.match(/^index ([0-9a-fA-F]+)\.\.([0-9a-fA-F]+)(?: (\d+))?/);
    if (indexMatch) {
      meta.blobs = { oldHash: indexMatch[1]!, newHash: indexMatch[2]! };
      if (indexMatch[3]) {
        meta.fileMode = modeLabel(indexMatch[3]);
        meta.isExecutable = indexMatch[3] === "100755";
      }
    }

    if (line.startsWith("new file mode ")) {
      meta.isNew = true;
      const m = line.replace("new file mode ", "").trim();
      meta.fileMode = modeLabel(m);
      if (m === "100755") meta.isExecutable = true;
    }

    if (line.startsWith("deleted file mode ")) {
      meta.isDeleted = true;
    }

    if (line.startsWith("old mode ")) oldMode = line.replace("old mode ", "").trim();
    if (line.startsWith("new mode ")) newMode = line.replace("new mode ", "").trim();
  }

  if (oldMode && newMode) {
    meta.modeChange = { oldMode: modeLabel(oldMode), newMode: modeLabel(newMode) };
  }

  return meta;
}

/** Splits a raw unified diff into per-file chunks with parsed lines. */
export function parseDiff(raw: string): ParsedFileChunk[] {
  if (!raw) return [];
  const parts = raw.split(/^diff --git /m);
  const chunks: ParsedFileChunk[] = [];

  for (const part of parts) {
    if (!part.trim()) continue;
    const rawLines = part.split("\n");
    const headerLine = rawLines[0]!;
    let filePath = "";
    const match = headerLine.match(/^a\/(.+?)\s+b\/(.+)$/);
    if (match) {
      filePath = match[2]!;
    } else {
      const fallbackMatch = headerLine.match(/b\/(.+)$/);
      filePath = fallbackMatch ? fallbackMatch[1]! : headerLine;
    }

    const metaLines: string[] = [];
    const lines: ParsedDiffLine[] = [];
    let oldLineNum = 1;
    let newLineNum = 1;

    for (let i = 1; i < rawLines.length; i++) {
      const line = rawLines[i]!;
      // Trailing empty element from the final newline — not a real diff line.
      if (i === rawLines.length - 1 && line === "") break;
      if (line.startsWith("@@")) {
        const hunkMatch = line.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
        if (hunkMatch) {
          oldLineNum = parseInt(hunkMatch[1]!, 10);
          newLineNum = parseInt(hunkMatch[2]!, 10);
        }
        lines.push({ type: "hunk", text: line });
      } else if (line.startsWith("+") && !line.startsWith("+++")) {
        lines.push({ type: "add", text: line, newLine: newLineNum++ });
      } else if (line.startsWith("-") && !line.startsWith("---")) {
        lines.push({ type: "delete", text: line, oldLine: oldLineNum++ });
      } else if (line.startsWith(" ") || line === "") {
        lines.push({ type: "context", text: line, oldLine: oldLineNum++, newLine: newLineNum++ });
      } else if (line.startsWith("\\")) {
        // "\ No newline at end of file" marker — keep it in the diff body
        // instead of letting it sink into metaLines and vanish.
        lines.push({ type: "note", text: line.trim() });
      } else if (!line.startsWith("--- ") && !line.startsWith("+++ ")) {
        metaLines.push(line);
      }
    }

    chunks.push({
      header: `diff --git ${headerLine}`,
      filePath,
      metaLines,
      lines,
    });
  }
  return chunks;
}
