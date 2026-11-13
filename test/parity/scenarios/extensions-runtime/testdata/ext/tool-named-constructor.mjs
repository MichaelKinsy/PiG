// Registers a tool whose name is a member of Object.prototype (scenario 98, divergence D645).
export default function (pi) {
  pi.registerTool({
    name: "constructor",
    label: "constructor",
    description: "A tool with this name.",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "ran constructor" }] };
    },
  });
}
