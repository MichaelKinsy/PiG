// parity-partial-observer: records every tool_execution_update partialResult a handler receives and, in a tool_result handler, replaces the bash result text with their JSON.stringify, so the JSON event stream shows exactly what a Node extension observed.
export default function (pi) {
  const seen = [];
  pi.on("tool_execution_update", (event) => {
    seen.push(event.partialResult);
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "bash") return;
    return { content: [{ type: "text", text: JSON.stringify(seen) }] };
  });
}
