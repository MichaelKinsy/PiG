// Registers a tool with an ordinary name: the control of the scenario with a tool named `constructor` (divergence D645).
export default function (pi) {
  pi.registerTool({
    name: "plain",
    label: "plain",
    description: "A tool with this name.",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "ran plain" }] };
    },
  });
}
