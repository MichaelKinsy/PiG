// The plugin of "connects servers registered while extensions load" and "prefers the mcp.json server over a registered %s"
// in packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2): it registers one server while it loads.
// MCP_PLUGIN_NAME names the server; MCP_PLUGIN_EXPOSURE sets its exposure (none: the server's default).
export default function (pi) {
  const config = { url: "http://plugin.invalid" };
  if (process.env.MCP_PLUGIN_EXPOSURE) config.exposure = process.env.MCP_PLUGIN_EXPOSURE;
  pi.registerMcpServer(process.env.MCP_PLUGIN_NAME ?? "plugin", config);
}
