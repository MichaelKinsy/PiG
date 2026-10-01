// The extension of "reports registered servers when no extension connects them" in
// packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2).
export default function (pi) {
  pi.registerMcpServer("orphan", { url: "http://orphan.invalid" });
}
