import { createSignal, createEffect, onCleanup, For, Show } from "solid-js";
import type { ProjectInfo, ServiceState } from "./types";
import { sendWS, onWS, subscribeLogs, wsStatus, connectWS } from "./ws";
import { createTerminal } from "./terminal";

export function App() {
  const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
  const [selectedProject, setSelectedProject] = createSignal<string | null>(null);
  const [services, setServices] = createSignal<ServiceState[]>([]);
  const [selectedService, setSelectedService] = createSignal<string | null>(null);

  let logContainer: HTMLDivElement | undefined;
  let term: ReturnType<typeof createTerminal> | null = null;

  createEffect(() => {
    connectWS();
    const off1 = onWS("projects", (resp) => setProjects(resp.data as ProjectInfo[]));
    const off2 = onWS("services", (resp) => {
      if (resp.project === selectedProject()) {
        setServices(resp.data as ServiceState[]);
      }
    });
    const poll = setInterval(() => {
      sendWS({ type: "list_projects" });
      if (selectedProject()) {
        sendWS({ type: "list_services", project: selectedProject()! });
      }
    }, 2000);
    sendWS({ type: "list_projects" });
    onCleanup(() => {
      off1();
      off2();
      clearInterval(poll);
    });
  });

  createEffect(() => {
    const proj = selectedProject();
    if (proj) {
      setServices([]);
      sendWS({ type: "list_services", project: proj });
    }
  });

  createEffect(() => {
    const svc = selectedService();
    if (!svc || !selectedProject() || !logContainer) return;

    if (term) term.dispose();
    term = createTerminal(logContainer!);

    const unsubscribe = subscribeLogs(selectedProject()!, svc, (line) => {
      term?.writeln(line);
    });

    onCleanup(() => {
      unsubscribe();
      term?.dispose();
      term = null;
    });
  });

  const statusColor = (status: string): string => {
    switch (status) {
      case "running": return "#4CAF50";
      case "starting": return "#FF9800";
      case "backoff": return "#FF5722";
      case "exited": return "#9E9E9E";
      case "stopped": return "#757575";
      default: return "#BDBDBD";
    }
  };

  const healthColor = (hasHealth: boolean, health: string): string => {
    if (!hasHealth) return "#9E9E9E";
    switch (health) {
      case "healthy": return "#4CAF50";
      case "unhealthy": return "#F44336";
      case "starting": return "#FF9800";
      default: return "#9E9E9E";
    }
  };

  return (
    <div style={{ display: "flex", height: "100vh", "font-family": "monospace", background: "#1e1e2e", color: "#cdd6f4" }}>
      {/* Project list */}
      <div style={{ width: "220px", borderRight: "1px solid #45475a", overflow: "auto" }}>
        <div style={{ padding: "12px 16px", "font-size": "0.7em", "text-transform": "uppercase", color: "#7f849c", "letter-spacing": "0.05em" }}>
          Projects
        </div>
        <For each={projects()}>
          {(p) => (
            <div
              onClick={() => setSelectedProject(p.name)}
              style={{
                padding: "8px 16px",
                cursor: "pointer",
                "background-color": selectedProject() === p.name ? "#313244" : "transparent",
                "border-left": selectedProject() === p.name ? "3px solid #89b4fa" : "3px solid transparent",
              }}
            >
              <div style={{ "font-weight": "bold", "font-size": "0.9em" }}>{p.name}</div>
              <div style={{ "font-size": "0.75em", color: statusColor(p.status) }}>{p.status}</div>
            </div>
          )}
        </For>
        <Show when={projects().length === 0}>
          <div style={{ padding: "16px", color: "#6c7086", "font-size": "0.85em" }}>
            No projects. Run <code>local-compose up</code> to start one.
          </div>
        </Show>
      </div>

      {/* Service table + logs */}
      <div style={{ flex: 1, display: "flex", "flex-direction": "column" }}>
        <Show when={selectedProject()} fallback={
          <div style={{ display: "flex", "align-items": "center", "justify-content": "center", height: "100%", color: "#6c7086" }}>
            Select a project
          </div>
        }>
          {/* Toolbar */}
          <div style={{ display: "flex", "align-items": "center", gap: "8px", padding: "8px 16px", "border-bottom": "1px solid #45475a" }}>
            <span style={{ "font-weight": "bold" }}>{selectedProject()}</span>
            <span style={{ "font-size": "0.75em", color: "#7f849c" }}>·</span>
            <button onClick={() => sendWS({ type: "stop_project", project: selectedProject()! })} style={btnStyle}>Stop</button>
            <span style={{ "margin-left": "auto", "font-size": "0.75em", color: wsStatus() === "open" ? "#4CAF50" : "#F44336" }}>
              {wsStatus()}
            </span>
          </div>

          {/* Service table */}
          <div style={{ overflow: "auto", "max-height": "40%" }}>
            <table style={{ width: "100%", "border-collapse": "collapse", "font-size": "0.85em" }}>
              <thead>
                <tr style={{ color: "#7f849c", "text-align": "left" }}>
                  <th style={thStyle}>Service</th>
                  <th style={thStyle}>Status</th>
                  <th style={thStyle}>PID</th>
                  <th style={thStyle}>Restarts</th>
                  <th style={thStyle}>Health</th>
                  <th style={thStyle}>Actions</th>
                </tr>
              </thead>
              <tbody>
                <For each={services()}>
                  {(s) => (
                    <tr
                      onClick={() => setSelectedService(s.name)}
                      style={{
                        cursor: "pointer",
                        "background-color": selectedService() === s.name ? "#313244" : "transparent",
                      }}
                    >
                      <td style={tdStyle}>{s.name}</td>
                      <td style={{ ...tdStyle, color: statusColor(s.status) }}>
                        {s.status}{s.status === "exited" ? ` (${s.exit_code})` : ""}
                      </td>
                      <td style={tdStyle}>{s.pid > 0 ? s.pid : "-"}</td>
                      <td style={tdStyle}>{s.restarts}</td>
                      <td style={{ ...tdStyle, color: healthColor(s.has_health, s.health) }}>
                        {s.has_health ? s.health : "-"}
                      </td>
                      <td style={tdStyle}>
                        <button
                          onClick={(e) => { e.stopPropagation(); sendWS({ type: "restart_service", project: selectedProject()!, service: s.name }); }}
                          style={smallBtnStyle}
                        >Restart</button>
                        {" "}
                        <button
                          onClick={(e) => { e.stopPropagation(); sendWS({ type: "stop_service", project: selectedProject()!, service: s.name }); }}
                          style={smallBtnStyle}
                        >Stop</button>
                        {" "}
                        <button
                          onClick={(e) => { e.stopPropagation(); sendWS({ type: "kill_service", project: selectedProject()!, service: s.name }); }}
                          style={smallBtnStyle}
                        >Kill</button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>

          {/* Log terminal */}
          <div style={{ flex: 1, padding: "8px", "overflow": "hidden" }}>
            <Show when={selectedService()} fallback={
              <div style={{ display: "flex", "align-items": "center", "justify-content": "center", height: "100%", color: "#6c7086" }}>
                Select a service to view logs
              </div>
            }>
              <div ref={logContainer} style={{ height: "100%", background: "#11111b", padding: "4px" }} />
            </Show>
          </div>
        </Show>
      </div>
    </div>
  );
}

const btnStyle: Record<string, string> = {
  "background": "#313244",
  "border": "1px solid #45475a",
  "color": "#cdd6f4",
  "padding": "4px 10px",
  "border-radius": "4px",
  "cursor": "pointer",
  "font-family": "monospace",
  "font-size": "0.8em",
};

const smallBtnStyle: Record<string, string> = {
  ...btnStyle,
  padding: "2px 6px",
  "font-size": "0.75em",
};

const thStyle: Record<string, string> = {
  padding: "6px 12px",
  "border-bottom": "1px solid #45475a",
};

const tdStyle: Record<string, string> = {
  padding: "6px 12px",
  "border-bottom": "1px solid #313244",
};
