import { describe, expect, it } from "vitest";
import { listKey } from "./nav";

describe("listKey", () => {
  it("steps with j/k and arrows, clamped to the list", () => {
    expect(listKey("j", 0, 3)).toEqual({ index: 1 });
    expect(listKey("ArrowDown", 2, 3)).toEqual({ index: 2 });
    expect(listKey("k", 1, 3)).toEqual({ index: 0 });
    expect(listKey("ArrowUp", 0, 3)).toEqual({ index: 0 });
  });

  it("starts at the first item when nothing is selected", () => {
    expect(listKey("j", -1, 3)).toEqual({ index: 0 });
    expect(listKey("k", -1, 3)).toEqual({ index: 0 });
    expect(listKey("Enter", -1, 3)).toBeNull();
  });

  it("jumps with Home/End and opens with Enter", () => {
    expect(listKey("Home", 2, 5)).toEqual({ index: 0 });
    expect(listKey("End", 0, 5)).toEqual({ index: 4 });
    expect(listKey("Enter", 1, 5)).toEqual({ open: true });
  });

  it("ignores other keys and empty lists", () => {
    expect(listKey("x", 0, 3)).toBeNull();
    expect(listKey("j", -1, 0)).toBeNull();
  });
});
