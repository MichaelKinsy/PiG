// A command with getArgumentCompletions, shaped like pi-mcp-adapter's /mcp.
export default function (pi) {
  pi.registerCommand("probe", {
    description: "Probe argument completions",
    getArgumentCompletions: (prefix) => {
      const normalized = prefix.trimStart();
      const items = [
        { value: "reconnect", label: "reconnect — Reconnect servers" },
        { value: "tools", label: "tools — List all tools" },
        { value: "status", label: "status — Show server status" },
      ].filter(({ value }) => value.startsWith(normalized));
      return items.length > 0 ? items : null;
    },
    handler: async (args, ctx) => {
      ctx.ui.notify(`probe ran with "${args}"`, "info");
    },
  });
}
