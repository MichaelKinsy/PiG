// parity-tool-call-member-order: Pi hands every tool_call handler the one event object, {type, toolName, toolCallId, input}, and runs the tool with its input (agent-session.ts _beforeToolCall; agent-loop.ts prepareToolCall). A handler that edits the command and adds a member leaves the input as {command, aaa}, which the tool and tool_result see in that order. The tool_result handler answers "[parity-modified]" only when the event and the input kept Pi's member order and the rewritten command ran; otherwise it reports what it saw.
export default function (pi) {
  let eventKeys = "";
  pi.on("tool_call", (event) => {
    if (event.toolName !== "bash") return;
    eventKeys = Object.keys(event).join(",");
    if (event.input.command === "echo parity-base") {
      event.input.command = "echo rewritten";
      event.input.aaa = 1;
    }
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "bash") return;
    const input = JSON.stringify(event.input);
    const ran = event.content[0].text.trim();
    const ok = eventKeys === "type,toolName,toolCallId,input" && input === '{"command":"echo rewritten","aaa":1}' && ran === "rewritten";
    return { content: [{ type: "text", text: ok ? "[parity-modified]" : `event=${eventKeys} input=${input} ran=${ran}` }] };
  });
}
