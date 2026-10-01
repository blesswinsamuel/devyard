import { describe, expect, it } from "vitest";
import { layoutGraph } from "./graph-layout";

describe("layoutGraph", () => {
  it("places dependencies in earlier layers", () => {
    const g = layoutGraph([
      { name: "web", deps: ["api"] },
      { name: "api", deps: ["db", "cache"] },
      { name: "db", deps: [] },
      { name: "cache", deps: [] },
    ]);
    const layer = Object.fromEntries(g.nodes.map((n) => [n.name, n.layer]));
    expect(layer).toEqual({ cache: 0, db: 0, api: 1, web: 2 });
    expect(g.layers).toBe(3);
    expect(g.rows).toBe(2);
    expect(g.edges).toHaveLength(3);
  });

  it("survives cycles and unknown dependencies", () => {
    const g = layoutGraph([
      { name: "a", deps: ["b", "ghost"] },
      { name: "b", deps: ["a"] },
    ]);
    expect(g.nodes).toHaveLength(2);
    expect(g.edges).toHaveLength(2);
  });
});
