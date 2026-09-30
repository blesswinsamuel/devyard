import { describe, expect, it } from "vitest";
import { computeGitGraph, graphWidth, laneColor, LANE_COLORS, type GraphCommit } from "./graph";

const c = (hash: string, ...parents: string[]): GraphCommit => ({ hash, parents });
const parentEdges = (info: { edges: { type: string; from: number; to: number }[] } | undefined) =>
  info?.edges.filter((e) => e.type === "parent").map((e) => [e.from, e.to]);
const passLanes = (info: { edges: { type: string; from: number }[] } | undefined) =>
  info?.edges.filter((e) => e.type === "pass").map((e) => e.from);

describe("computeGitGraph", () => {
  it("keeps linear history in one lane and color", () => {
    const g = computeGitGraph([c("c3", "c2"), c("c2", "c1"), c("c1")]);
    for (const h of ["c3", "c2", "c1"]) {
      expect(g.get(h)?.column).toBe(0);
      expect(g.get(h)?.colorIndex).toBe(0);
      expect(passLanes(g.get(h))).toEqual([]);
    }
    expect(g.get("c3")?.incoming).toBe(false);
    expect(g.get("c2")?.incoming).toBe(true);
    expect(parentEdges(g.get("c3"))).toEqual([[0, 0]]);
    // The root closes its lane: no edges below it.
    expect(parentEdges(g.get("c1"))).toEqual([]);
    expect(graphWidth(g)).toBe(1);
  });

  it("routes a merge's second parent into a new lane that joins back at the fork", () => {
    //   M        (merge of A and B)
    //   |\
    //   A |
    //   | B
    //   |/
    //   F
    const g = computeGitGraph([c("M", "A", "B"), c("A", "F"), c("B", "F"), c("F")]);
    const m = g.get("M")!;
    expect(m.column).toBe(0);
    expect(parentEdges(m)).toEqual([
      [0, 0],
      [0, 1],
    ]);
    expect(m.activeWidth).toBe(2);

    const a = g.get("A")!;
    expect(a.column).toBe(0);
    expect(passLanes(a)).toEqual([1]);

    const b = g.get("B")!;
    expect(b.column).toBe(1);
    expect(b.incoming).toBe(true);
    // F is already tracked in lane 0: B joins it instead of opening a duplicate.
    expect(parentEdges(b)).toEqual([[1, 0]]);
    expect(b.activeWidth).toBe(2);

    expect(g.get("F")?.column).toBe(0);
    expect(g.get("F")?.activeWidth).toBe(1);
    expect(g.get("F")?.edges).toEqual([]);
    expect(graphWidth(g)).toBe(2);
  });

  it("gives branches their own color and the first parent inherits it", () => {
    const g = computeGitGraph([c("M", "A", "B"), c("B", "B0"), c("A", "F"), c("B0", "F"), c("F")]);
    const main = g.get("M")!.colorIndex;
    const side = g.get("B")!.colorIndex;
    expect(side).not.toBe(main);
    expect(g.get("A")!.colorIndex).toBe(main);
    expect(g.get("B0")!.colorIndex).toBe(side);
    expect(g.get("B0")!.column).toBe(1);
    // A runs in lane 0 while B's lane passes through.
    expect(passLanes(g.get("A"))).toEqual([1]);
  });

  it("opens a new lane for a second branch tip and reuses freed lanes", () => {
    // Two tips (T1, T2) on top of a shared base, listed newest first.
    const g = computeGitGraph([c("T1", "X"), c("T2", "X"), c("X", "R"), c("S", "R"), c("R")]);
    expect(g.get("T1")!.column).toBe(0);
    expect(g.get("T2")!.column).toBe(1);
    expect(g.get("T2")!.incoming).toBe(false);
    expect(parentEdges(g.get("T2"))).toEqual([[1, 0]]);
    // Lane 1 was freed by T2's join, so the next tip S reuses it.
    expect(g.get("S")!.column).toBe(1);
    expect(parentEdges(g.get("S"))).toEqual([[1, 0]]);
  });

  it("handles the WORKDIR pseudo-commit on top of HEAD", () => {
    const g = computeGitGraph([c("WORKDIR", "h2"), c("h2", "h1"), c("h1")]);
    expect(g.get("WORKDIR")).toMatchObject({ column: 0, incoming: false });
    expect(g.get("h2")).toMatchObject({ column: 0, incoming: true });
  });

  it("keeps a lane open for parents outside the loaded window", () => {
    const g = computeGitGraph([c("a", "b", "missing"), c("b", "z")]);
    expect(passLanes(g.get("b"))).toEqual([1]);
    expect(g.get("b")!.activeWidth).toBe(2);
  });

  it("maps color indexes onto the source palette", () => {
    expect(laneColor(0)).toBe("var(--source-1)");
    expect(laneColor(LANE_COLORS)).toBe("var(--source-1)");
    expect(laneColor(3)).toBe("var(--source-4)");
  });
});
