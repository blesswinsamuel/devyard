import { describe, expect, it } from "vitest";
import { backoffDelay } from "./backoff";

describe("backoffDelay", () => {
  const opts = (r: number) => ({ baseMs: 500, maxMs: 8000, random: () => r });
  it("doubles from 500ms and caps at 8s", () => {
    expect([1, 2, 3, 4, 5, 6, 10].map((n) => backoffDelay(n, opts(0.999999)))).toEqual([
      500, 1000, 2000, 4000, 8000, 8000, 8000,
    ]);
  });
  it("jitters within [d/2, d)", () => {
    expect(backoffDelay(1, opts(0))).toBe(250);
    expect(backoffDelay(5, opts(0))).toBe(4000);
    expect(backoffDelay(3, opts(0.5))).toBe(1500);
  });
});
