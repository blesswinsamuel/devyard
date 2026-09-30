import { describe, expect, it } from "vitest";
import { layoutGraph } from "./graph-layout";

describe("layoutGraph", () => {
  it("places dependencies in earlier layers", () => {
    const g = layoutGraph([
      { name: "web", deps: [{ name: "api", condition: "service_started" }] },
      { name: "api", deps: [{ name: "db", condition: "service_healthy" }, { name: "cache", condition: "service_started" }] },
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
      { name: "a", deps: [{ name: "b", condition: "" }, { name: "ghost", condition: "" }] },
      { name: "b", deps: [{ name: "a", condition: "" }] },
    ]);
    expect(g.nodes).toHaveLength(2);
    expect(g.edges).toHaveLength(2);
  });
});
