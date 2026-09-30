import { beforeEach, describe, expect, it } from "vitest";
import { dock, dockActions, setDock } from "./dock";

describe("dock state", () => {
  beforeEach(() => setDock({ open: false, height: 320, tabs: [], panes: [null, null], focused: 0 }));

  it("opens tabs into the focused pane and reuses attach tabs", () => {
    dockActions.openTerminal("shop");
    expect(dock.open).toBe(true);
    expect(dock.tabs[0]).toMatchObject({ kind: "terminal", project: "shop", title: "shop" });
    dockActions.openTerminal("shop");
    expect(dock.tabs[1]?.title).toBe("shop (2)");
    dockActions.attach("task", "shop", "migrate");
    dockActions.attach("task", "shop", "migrate");
    expect(dock.tabs.filter((t) => t.kind === "attach")).toHaveLength(1);
    expect(dock.panes[0]).toBe(dock.tabs[2]!.id);
  });

  it("splits, focuses panes and closes back to one pane", () => {
    dockActions.openTerminal("a");
    dockActions.openTerminal("b");
    const [a, b] = dock.tabs.map((t) => t.id);
    expect(dock.panes).toEqual([b, null]);
    dockActions.toggleSplit();
    expect(dock.panes).toEqual([b, a]);
    expect(dock.focused).toBe(1);
    dockActions.close(a!);
    expect(dock.panes).toEqual([b, null]);
    expect(dock.focused).toBe(0);
    dockActions.close(b!);
    expect(dock.open).toBe(false);
    expect(dock.panes).toEqual([null, null]);
  });

  it("remembers terminal session ids and persists", () => {
    dockActions.openTerminal("a");
    const id = dock.tabs[0]!.id;
    dockActions.setSessionId(id, "sess-1");
    expect(dock.tabs[0]).toMatchObject({ sessionId: "sess-1" });
    const saved = JSON.parse(localStorage.getItem("devyard:v1:dock") ?? "{}");
    expect(saved.tabs[0]).toMatchObject({ id, sessionId: "sess-1" });
  });
});
