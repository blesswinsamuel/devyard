import { describe, expect, it } from "vitest";
import { isTypingTarget, matchShortcut, shortcutKeys } from "./keyboard";

const key = (k: string, mods: Partial<Record<"metaKey" | "ctrlKey" | "altKey" | "shiftKey", boolean>> = {}) => ({
  key: k,
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...mods,
});

describe("matchShortcut", () => {
  it("matches single keys without modifiers", () => {
    expect(matchShortcut(key("r"), "r")).toBe(true);
    expect(matchShortcut(key("R", { shiftKey: true }), "r")).toBe(false);
    expect(matchShortcut(key("r", { metaKey: true }), "r")).toBe(false);
  });
  it("maps mod to meta on mac and ctrl elsewhere", () => {
    expect(matchShortcut(key("k", { metaKey: true }), "mod+k", true)).toBe(true);
    expect(matchShortcut(key("k", { ctrlKey: true }), "mod+k", true)).toBe(false);
    expect(matchShortcut(key("k", { ctrlKey: true }), "mod+k", false)).toBe(true);
  });
  it("ignores shift for symbol keys", () => {
    expect(matchShortcut(key("?", { shiftKey: true }), "?")).toBe(true);
  });
  it("renders key caps", () => {
    expect(shortcutKeys("mod+k", true)).toEqual(["⌘", "K"]);
    expect(shortcutKeys("mod+k", false)).toEqual(["Ctrl", "K"]);
  });
});

describe("isTypingTarget", () => {
  it("detects inputs, textareas and terminals", () => {
    const input = document.createElement("input");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    const term = document.createElement("div");
    term.className = "xterm";
    const inner = document.createElement("textarea");
    term.appendChild(inner);
    expect(isTypingTarget(input)).toBe(true);
    expect(isTypingTarget(checkbox)).toBe(false);
    expect(isTypingTarget(inner)).toBe(true);
    expect(isTypingTarget(document.createElement("button"))).toBe(false);
  });
});
