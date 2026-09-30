import { describe, expect, it } from "vitest";
import { color256, parseAnsi, stripAnsi, styleToCss } from "./ansi";

describe("parseAnsi", () => {
  it("returns plain text as one unstyled segment", () => {
    expect(parseAnsi("hello world")).toEqual([{ text: "hello world" }]);
  });

  it("parses basic and bright foreground colours with reset", () => {
    expect(parseAnsi("\x1b[31mred\x1b[0m plain \x1b[92mgreen")).toEqual([
      { text: "red", style: { fg: "var(--ansi-red)" } },
      { text: " plain " },
      { text: "green", style: { fg: "var(--ansi-bright-green)" } },
    ]);
  });

  it("treats an empty SGR as reset", () => {
    expect(parseAnsi("\x1b[1mbold\x1b[m done")).toEqual([
      { text: "bold", style: { bold: true } },
      { text: " done" },
    ]);
  });

  it("combines attributes and clears them selectively", () => {
    const segs = parseAnsi("\x1b[1;3;4;41mA\x1b[22mB\x1b[24;49mC");
    expect(segs).toEqual([
      { text: "A", style: { bold: true, italic: true, underline: true, bg: "var(--ansi-red)" } },
      { text: "B", style: { italic: true, underline: true, bg: "var(--ansi-red)" } },
      { text: "C", style: { italic: true } },
    ]);
  });

  it("supports 256-colour and truecolor (semicolon and colon forms)", () => {
    expect(parseAnsi("\x1b[38;5;196mx")[0]!.style).toEqual({ fg: "rgb(255, 0, 0)" });
    expect(parseAnsi("\x1b[48;5;244mx")[0]!.style).toEqual({ bg: "rgb(128, 128, 128)" });
    expect(parseAnsi("\x1b[38;2;10;20;30mx")[0]!.style).toEqual({ fg: "rgb(10, 20, 30)" });
    expect(parseAnsi("\x1b[38:2::1:2:3mx")[0]!.style).toEqual({ fg: "rgb(1, 2, 3)" });
    expect(parseAnsi("\x1b[38:5:4mx")[0]!.style).toEqual({ fg: "var(--ansi-blue)" });
  });

  it("continues parsing params after an extended colour", () => {
    expect(parseAnsi("\x1b[38;5;1;1mx")[0]!.style).toEqual({ fg: "var(--ansi-red)", bold: true });
  });

  it("resets only the fg with 39", () => {
    expect(parseAnsi("\x1b[31;1mA\x1b[39mB")).toEqual([
      { text: "A", style: { fg: "var(--ansi-red)", bold: true } },
      { text: "B", style: { bold: true } },
    ]);
  });

  it("drops non-SGR CSI, OSC and control characters", () => {
    expect(stripAnsi("a\x1b[2Kb\x1b[1;1Hc\x1b]0;title\x07d\x1b]8;;http://x\x1b\\e\rf\x08g\th")).toBe("abcdefg\th");
  });

  it("merges adjacent segments with the same style", () => {
    expect(parseAnsi("\x1b[31ma\x1b[31mb")).toEqual([{ text: "ab", style: { fg: "var(--ansi-red)" } }]);
  });

  it("handles a truncated escape at end of input", () => {
    expect(stripAnsi("abc\x1b[31")).toBe("abc");
    expect(stripAnsi("abc\x1b")).toBe("abc");
  });
});

describe("color256", () => {
  it("maps the cube and grayscale ramp", () => {
    expect(color256(16)).toBe("rgb(0, 0, 0)");
    expect(color256(231)).toBe("rgb(255, 255, 255)");
    expect(color256(232)).toBe("rgb(8, 8, 8)");
    expect(color256(9)).toBe("var(--ansi-bright-red)");
    expect(color256(300)).toBeUndefined();
  });
});

describe("styleToCss", () => {
  it("swaps colours for inverse", () => {
    expect(styleToCss({ inverse: true, fg: "red" })).toEqual({ color: "var(--background)", "background-color": "red" });
  });
  it("combines decorations", () => {
    expect(styleToCss({ underline: true, strike: true })["text-decoration"]).toBe("underline line-through");
  });
});
