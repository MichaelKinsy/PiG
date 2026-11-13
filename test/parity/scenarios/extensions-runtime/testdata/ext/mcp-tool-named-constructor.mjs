// Registers a stdio MCP server named `docs` with exposure `direct` whose tools are `constructor`, a member name of
// Object.prototype, and `search` (scenario 99, divergence D645). The server's script travels inline (`node -e`).
const SERVER = String.raw`
const readline = require("node:readline");
const send = (message) => process.stdout.write(JSON.stringify(message) + "\n");
const schema = { type: "object", properties: { query: { type: "string" } }, required: ["query"] };
readline.createInterface({ input: process.stdin }).on("line", (line) => {
  const message = JSON.parse(line);
  if (message.id === undefined) return;
  let result = {};
  if (message.method === "initialize") {
    result = { protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "docs", version: "1.0.0" } };
  } else if (message.method === "tools/list") {
    result = { tools: [{ name: "constructor", description: "A tool named constructor.", inputSchema: schema }, { name: "search", description: "Search the docs.", inputSchema: schema }] };
  } else if (message.method === "tools/call") {
    result = { content: [{ type: "text", text: "mcp-hit: " + message.params.name + " " + message.params.arguments.query }] };
  }
  send({ jsonrpc: "2.0", id: message.id, result });
});
`;

export default function (pi) {
  pi.registerMcpServer("docs", { command: "node", args: ["-e", SERVER], exposure: "direct" });
}
