import { describe, expect, it } from "vitest";
import { crashReason, taskFailureReason } from "./crash";
import { toService, toTask } from "./entities";
import { service, task } from "~/test/fixtures";

const s = (over: Parameters<typeof service>[0]) => toService(service(over));

describe("crashReason", () => {
  it("fires when entering a failing state", () => {
    expect(crashReason(s({}), s({ status: "exited", exitCode: 2 }))).toBe("exited with code 2");
    expect(crashReason(s({}), s({ status: "backoff", message: "restarting in 4s" }))).toBe("restarting in 4s");
    expect(crashReason(s({ health: "healthy" }), s({ health: "unhealthy", healthDetail: "connection refused" }))).toBe(
      "unhealthy: connection refused",
    );
  });
  it("fires when moving between failing states", () => {
    expect(crashReason(s({ status: "backoff" }), s({ status: "failed", message: "gave up after 5 restarts" }))).toBe(
      "gave up after 5 restarts",
    );
  });
  it("stays quiet for clean exits, recoveries and repeats", () => {
    expect(crashReason(s({}), s({ status: "exited", exitCode: 0 }))).toBeNull();
    expect(crashReason(s({ status: "failed" }), s({ status: "failed", message: "x" }))).toBeNull();
    expect(crashReason(s({ status: "backoff" }), s({ status: "starting" }))).toBeNull();
  });
});

describe("taskFailureReason", () => {
  it("fires once per failed run", () => {
    const t = (over: Parameters<typeof task>[0]) => toTask(task(over));
    expect(taskFailureReason(t({ status: "running", run: 1n }), t({ status: "exited", exitCode: 1, run: 1n }))).toBe(
      "exited with code 1",
    );
    expect(taskFailureReason(t({ status: "exited", exitCode: 1, run: 1n }), t({ status: "exited", exitCode: 1, run: 1n }))).toBeNull();
    expect(taskFailureReason(t({ status: "running" }), t({ status: "exited", exitCode: 0 }))).toBeNull();
  });
});
