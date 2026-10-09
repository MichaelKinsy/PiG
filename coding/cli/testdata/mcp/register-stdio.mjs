// Registers one stdio MCP server while the extension loads, as an extension that bundles a server does
// (pi.registerMcpServer, extensions/types.ts). The command, arguments and log file come from the test.
export default function (pi) {
  pi.registerMcpServer("bundled", {
    command: process.env.MCP_REGISTER_COMMAND,
    args: ["bundled"],
    env: { MCP_FIXTURE_LOG: process.env.MCP_REGISTER_LOG },
  });
}
