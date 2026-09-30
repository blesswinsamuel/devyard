import { describe, expect, it } from "vitest";
import { joinArgs, splitArgs } from "./args";

describe("splitArgs", () => {
  it("splits on whitespace", () => {
    expect(splitArgs("  a  b\tc ")).toEqual({ args: ["a", "b", "c"] });
  });
  it("handles quotes and escapes", () => {
    expect(splitArgs(`--name "hello world" 'it''s' a\\ b "q\\"x" ''`)).toEqual({
      args: ["--name", "hello world", "its", "a b", 'q"x', ""],
    });
  });
  it("reports unterminated quotes", () => {
    expect(splitArgs(`"oops`)).toEqual({ error: "Unterminated double quote" });
  });
  it("round-trips through joinArgs", () => {
    const args = ["plain", "with space", "it's", ""];
    expect(splitArgs(joinArgs(args))).toEqual({ args });
  });
});
