/** Probe the live web WS endpoint like the browser would. */
const port = process.argv[2] ?? "9090";
const ws = new WebSocket(`ws://127.0.0.1:${port}/ws`);
ws.onmessage = (e) => {
  const d = JSON.parse(e.data);
  console.log("RECV", JSON.stringify(d).slice(0, 400));
};
ws.onopen = () => {
  console.log("OPEN");
  const proj = process.argv[3] ?? "";
  ws.send(JSON.stringify({ type: "list_projects" }));
  setTimeout(() => ws.send(JSON.stringify({ type: "list_ports", project: proj })), 200);
};
ws.onclose = () => console.log("CLOSED");
ws.onerror = () => console.log("ERROR");
setTimeout(() => process.exit(0), 2500);
