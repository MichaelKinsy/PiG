// The extension of "connects and disconnects servers registered during the session" in
// packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2): the test keeps `pi` and registers a server after
// the extensions are bound. The Session cannot hand `pi` to the test, so the tools register_late and unregister_late call
// `pi.registerMcpServer` and `pi.unregisterMcpServer`, and the test runs them as a script of the model would.
import { Type } from "typebox";

export default function (pi) {
  pi.registerTool({
    name: "register_late",
    label: "register_late",
    description: "Registers the MCP server `late`.",
    parameters: Type.Object({}),
    execute: async () => {
      pi.registerMcpServer("late", { url: "http://late.invalid" });
      return { content: [{ type: "text", text: "registered" }], details: {} };
    },
  });
  pi.registerTool({
    name: "unregister_late",
    label: "unregister_late",
    description: "Unregisters the MCP server `late`.",
    parameters: Type.Object({}),
    execute: async () => {
      pi.unregisterMcpServer("late");
      return { content: [{ type: "text", text: "unregistered" }], details: {} };
    },
  });
}
