// parity-tool-call-rewriter: a command rewriter. Its tool_call handler edits event.input in place, which is how Pi's extensions rewrite a call (runner.ts emitToolCall hands every handler the one event object and the agent loop runs the tool with its input). The tool_result handler echoes the input it sees, which is the edited object in Pi.
export default function (pi) {
  pi.on("tool_call", (event) => {
    if (event.toolName !== "bash") return;
    if (event.input.command === "echo parity-base") {
      event.input.command = "echo '[parity-modified]'";
    }
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "bash") return;
    return { content: [{ type: "text", text: `${event.content[0].text.trim()} input=${event.input.command}` }] };
  });
}
