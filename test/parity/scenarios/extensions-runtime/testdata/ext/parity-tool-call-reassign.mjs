// parity-tool-call-reassign: a tool_call handler assigns a new object to event.input instead of editing it. Pi runs the tool with the object the event carried when the handlers started (agent-loop.ts prepareToolCall passes the same args to beforeToolCall and execute), so the call runs `echo parity-base` and tool_result sees that input. The tool_result handler answers "[parity-modified]" only then, so a host that applies the new object answers "not-modified".
export default function (pi) {
  pi.on("tool_call", (event) => {
    if (event.toolName !== "bash") return;
    if (event.input.command === "echo parity-base") {
      event.input = { ...event.input, command: "echo reassigned" };
    }
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "bash") return;
    const ran = event.content[0].text.trim();
    const text = ran === "parity-base" && event.input.command === "echo parity-base" ? "[parity-modified]" : `ran=${ran} input=${event.input.command}`;
    return { content: [{ type: "text", text }] };
  });
}
