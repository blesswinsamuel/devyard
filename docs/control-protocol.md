# Control Protocol (ConnectRPC)

The communication between the global daemon and its clients (`local-compose ps`/`logs`/`restart`/`down`/`top`, CLI commands, and the web UI) is powered by **ConnectRPC** over HTTP/2 on Unix domain sockets and TCP.

Implementation:
- Protobuf schema: `proto/localcompose/v1/control.proto`
- Go code generation: `internal/gen/proto/localcompose/v1/`
- TypeScript code generation: `web/src/gen/localcompose/v1/`
- Server: `internal/control/server.go`
- Client: `internal/control/client.go`

## Transport

- **Daemon Socket**: Unix domain socket at `$XDG_RUNTIME_DIR/local-compose/daemon.sock` (or `/tmp/local-compose/daemon.sock`).
- **HTTP/2 Transport**: Client and server communicate using HTTP/2 cleartext (h2c) over the Unix socket.
- **Multiplexing**: A single connection can serve multiple parallel requests and streams concurrently.

## Schema & Service Definition

The protocol is defined in `proto/localcompose/v1/control.proto` under the `localcompose.v1` package:

```protobuf
service DaemonService {
  // Project operations
  rpc ListProjects(ListProjectsRequest) returns (ListProjectsResponse);
  rpc StartProject(StartProjectRequest) returns (StartProjectResponse);
  rpc StopProject(StopProjectRequest) returns (StopProjectResponse);
  rpc RemoveProject(RemoveProjectRequest) returns (RemoveProjectResponse);

  // Daemon lifecycle & status
  rpc DaemonStatus(DaemonStatusRequest) returns (DaemonStatusResponse);
  rpc StopDaemon(StopDaemonRequest) returns (StopDaemonResponse);
  rpc RestartDaemon(RestartDaemonRequest) returns (RestartDaemonResponse);

  // Service operations
  rpc ListServices(ListServicesRequest) returns (ListServicesResponse);
  rpc StartService(StartServiceRequest) returns (StartServiceResponse);
  rpc StopService(StopServiceRequest) returns (StopServiceResponse);
  rpc KillService(KillServiceRequest) returns (KillServiceResponse);
  rpc Restart(RestartRequest) returns (RestartResponse);
  rpc Top(TopRequest) returns (TopResponse);
  rpc ListPorts(ListPortsRequest) returns (ListPortsResponse);

  // Actions
  rpc ListActions(ListActionsRequest) returns (ListActionsResponse);
  rpc RunAction(RunActionRequest) returns (stream ActionOutputChunk);

  // Logs & Rotation
  rpc Logs(LogsRequest) returns (stream LogChunk);

  // Git operations
  rpc GitLog(GitLogRequest) returns (GitLogResponse);
  rpc GitDiff(GitDiffRequest) returns (GitDiffResponse);
  rpc GitCommit(GitCommitRequest) returns (GitCommitResponse);
  rpc GitStage(GitStageRequest) returns (GitStageResponse);
  rpc GitPush(GitPushRequest) returns (GitPushResponse);
  rpc GitPull(GitPullRequest) returns (GitPullResponse);
  rpc GitFetch(GitFetchRequest) returns (GitFetchResponse);

  // Real-time Event Streaming
  rpc SubscribeEvents(SubscribeEventsRequest) returns (stream Event);
}
```

## Streaming Behavior

### Logs & Action Logs

The `Logs` RPC streams `LogChunk` messages:
- **History**: When requested, initial history is sent in `content`.
- **Streaming & Batching**: New log lines are batched into `lines: []string` slices, significantly reducing framing and syscall overhead.
- **Rotation**: When a log file is rotated (e.g. on service restart or truncation), a `LogChunk` with `rotated: true` is emitted.

### Event Subscription

The `SubscribeEvents` server-streaming RPC streams real-time updates for:
- Service state changes (`ServiceStateChangedEvent`)
- Action state changes (`ActionStateChangedEvent`)
- Git repository changes (`GitChangedEvent`)
- Project lifecycle changes (`ProjectsChangedEvent`: projects started, stopped, or removed)
- Keepalive heartbeats (`HeartbeatEvent`: emitted immediately on subscription to flush headers and periodically every 30s)

## Web UI Integration

The embedded web UI server reverse-proxies `/localcompose.v1.DaemonService/` directly to the daemon's Unix socket over HTTP/2, enabling native Connect-ES / Connect-Web clients in the browser with full streaming and bidirectional capabilities.
