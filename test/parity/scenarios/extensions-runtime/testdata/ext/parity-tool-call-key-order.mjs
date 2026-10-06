// parity-tool-call-key-order: a tool_call handler edits event.input in place (sets a member the model wrote, deletes one, adds one). Pi hands every handler the model's arguments object and runs the tool with it, so the members keep the order the model wrote them in, a retained member keeps its place and a new member follows (JSON.stringify of the edited object). The handlers print what they and the tool saw: the keys of the tool_call event, the input as the handler received it, the input the tool ran with, and the tool_result event's input.
let seen = "";
export default function (pi) {
  pi.registerTool({
    name: "key_order_probe",
    description: "Reports the arguments it runs with.",
    parameters: {
      type: "object",
      properties: { zeta: { type: "number" }, drop: { type: "boolean" }, alpha: { type: "number" }, mid: { type: "number" } },
    },
    async execute(_toolCallId, params) {
      return { content: [{ type: "text", text: `ran=${JSON.stringify(params)} keys=${Object.keys(params).join(",")}` }] };
    },
  });
  pi.on("tool_call", (event) => {
    if (event.toolName !== "key_order_probe") return;
    seen = `call=${Object.keys(event).join(",")} in=${JSON.stringify(event.input)}`;
    event.input.mid = 3;
    event.input.zeta = 9;
    delete event.input.drop;
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "key_order_probe") return;
    return { content: [{ type: "text", text: `${seen} ${event.content[0].text} out=${JSON.stringify(event.input)}` }] };
  });
}
