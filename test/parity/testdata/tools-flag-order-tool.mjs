// Registers ordered_tool, an extension tool that --tools can place between built-in tools.
export default function toolsFlagOrderTool(pi) {
  pi.registerTool({
    name: "ordered_tool",
    label: "Ordered",
    description: "Extension tool placed by the --tools order.",
    parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: "ok" }] }),
  });
}
