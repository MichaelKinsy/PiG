// parity-tool-call-reorder: a tool_call handler moves one member of event.input to the end (delete, then assign the same value) and changes nothing else. Pi runs the tool with the object the handler edited, so the move alone changes the member order the tool and tool_result see. The handlers print the input as the handler received it, the input the tool ran with, and the tool_result event's input.
let seen = "";
export default function (pi) {
  pi.registerTool({
    name: "key_order_probe",
    description: "Reports the arguments it runs with.",
    parameters: {
      type: "object",
      properties: { zeta: { type: "number" }, drop: { type: "boolean" }, alpha: { type: "number" } },
    },
    async execute(_toolCallId, params) {
      return { content: [{ type: "text", text: `ran=${JSON.stringify(params)} keys=${Object.keys(params).join(",")}` }] };
    },
  });
  pi.on("tool_call", (event) => {
    if (event.toolName !== "key_order_probe") return;
    seen = `in=${JSON.stringify(event.input)}`;
    const zeta = event.input.zeta;
    delete event.input.zeta;
    event.input.zeta = zeta;
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "key_order_probe") return;
    return { content: [{ type: "text", text: `${seen} ${event.content[0].text} out=${JSON.stringify(event.input)}` }] };
  });
}
