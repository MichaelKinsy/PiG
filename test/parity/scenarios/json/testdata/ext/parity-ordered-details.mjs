// parity-ordered-details: an extension tool whose partial result and details, and a tool_result handler's replacement details, are objects whose members are not in alphabetical order. The handler also reports what it received as the tool_result event's content and details.
export default function (pi) {
  pi.registerTool({
    name: "echo_bridge",
    description: "Echo back the provided text with ordered details.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    async execute(_toolCallId, params, _signal, onUpdate) {
      onUpdate?.({ content: [{ type: "text", text: "partial" }], details: { zeta: 1, alpha: { yy: 2, bb: 3 }, mid: [{ qq: 1, aa: 2 }] } });
      return { content: [{ type: "text", text: `echo-bridge: ${params.text}` }], details: { zeta: 1, alpha: { yy: 2, bb: 3 }, mid: [{ qq: 1, aa: 2 }] } };
    },
  });
  pi.on("tool_result", (event) => {
    if (event.toolName !== "echo_bridge") return;
    return {
      content: [{ type: "text", text: `echo-bridge: hello ${JSON.stringify(event.content)} ${JSON.stringify(event.details)}` }],
      details: { omega: 1, beta: { zz: 2, cc: 3 } },
    };
  });
}
