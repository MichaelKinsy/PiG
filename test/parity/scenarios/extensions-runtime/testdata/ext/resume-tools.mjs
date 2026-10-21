// Registers tools a resumed branch never mentioned (fresh), one it removed (retired) and one registered with defaultActive false (quiet), and reports the active tools.
export default function (pi) {
  for (const name of ["fresh", "retired", "quiet"]) {
    pi.registerTool({
      name,
      label: name,
      description: name,
      parameters: { type: "object", properties: {} },
      ...(name === "quiet" ? { defaultActive: false } : {}),
      execute: async () => ({ content: [{ type: "text", text: name }] }),
    });
  }
  pi.registerCommand("active-tools", {
    description: "Report the active tools.",
    handler: async (_args, ctx) => ctx.ui.notify(`active-tools:${pi.getActiveTools().sort().join(",")}:end`, "info"),
  });
}
