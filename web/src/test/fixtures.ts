import {
  Change,
  DaemonInfo,
  EntityRef,
  GitStatus,
  Heartbeat,
  Project,
  Service,
  ServiceSpec,
  Snapshot,
  Task,
  TaskSpec,
  WatchResponse,
} from "~/gen/devyard/v1/control_pb";
import type { PartialMessage } from "@bufbuild/protobuf";

export const project = (p: PartialMessage<Project> = {}) =>
  new Project({ id: "web", configPath: "/src/web/devyard.yml", hasConfig: true, status: "running", desired: "running", ...p });

export const service = (s: PartialMessage<Service> = {}) =>
  new Service({
    project: "web",
    name: "api",
    status: "running",
    run: 1n,
    spec: new ServiceSpec({ command: "go run .", restart: "on-failure" }),
    ...s,
  });

export const task = (t: PartialMessage<Task> = {}) =>
  new Task({ project: "web", name: "migrate", status: "idle", spec: new TaskSpec({ command: "make migrate", tty: true }), ...t });

export const git = (g: PartialMessage<GitStatus> = {}) =>
  new GitStatus({ project: "web", isRepo: true, branch: "main", changeSeq: 1n, ...g });

export const snapshot = (revision: number, s: PartialMessage<Snapshot>) =>
  new WatchResponse({
    revision: BigInt(revision),
    event: { case: "snapshot", value: new Snapshot({ daemon: new DaemonInfo({ pid: 42, version: "dev" }), ...s }) },
  });

export const change = (revision: number, c: Change["change"]) =>
  new WatchResponse({ revision: BigInt(revision), event: { case: "change", value: new Change({ change: c }) } });

export const removed = (revision: number, kind: string, projectId: string, name = "") =>
  change(revision, { case: "removed", value: new EntityRef({ kind, project: projectId, name }) });

export const heartbeat = (revision: number) =>
  new WatchResponse({ revision: BigInt(revision), event: { case: "heartbeat", value: new Heartbeat() } });
