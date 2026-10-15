// Reports what the session's system messages hold. The MCP extension's section reaches the transcript through the
// system message the first prompt appends. `/mcp-section` lists the section names of each system message;
// `/mcp-section-text` prints the text of the `mcp_servers` section.
export default function (pi: any) {
  const systems = (ctx: any) => ctx.sessionManager.getEntries().filter((entry: any) => entry.message?.role === "system");
  pi.registerCommand("mcp-section", {
    description: "Report the sections of the session's system messages",
    handler: async (_args: string, ctx: any) => {
      const report = systems(ctx).map((entry: any) => Object.keys(entry.message.sections ?? {}).join(",")).join(" | ");
      ctx.ui.notify(`mcp-section: ${report}\nmcp-section-end`, "info");
    },
  });
  pi.registerCommand("mcp-section-text", {
    description: "Print the mcp_servers section",
    handler: async (_args: string, ctx: any) => {
      const text = systems(ctx).map((entry: any) => entry.message.sections?.mcp_servers).filter(Boolean).at(-1);
      ctx.ui.notify(`mcp-text-begin\n${text ?? "(none)"}\nmcp-text-end`, "info");
    },
  });
}
