// Registers a stdio MCP server named `docs` with one direct tool, `search`, whose script travels inline (`node -e`), so
// the scenario needs no file next to the agent directory. The tool answers with a text hit.
const SERVER = String.raw`
const readline = require("node:readline");
const send = (message) => process.stdout.write(JSON.stringify(message) + "\n");
readline.createInterface({ input: process.stdin }).on("line", (line) => {
  const message = JSON.parse(line);
  if (message.id === undefined) return;
  let result = {};
  if (message.method === "initialize") {
    result = { protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "docs", version: "1.0.0" } };
  } else if (message.method === "tools/list") {
    result = { tools: [{ name: "search", description: "Search the docs.", inputSchema: { type: "object", properties: { query: { type: "string" } }, required: ["query"] } }] };
  } else if (message.method === "tools/call") {
    const query = message.params.arguments.query;
    result = { content: [{ type: "text", text: "mcp-hit: " + query + " guide\nmcp-hit: " + query + " faq" }] };
  }
  send({ jsonrpc: "2.0", id: message.id, result });
});
`;

export default function (pi: any) {
  pi.registerMcpServer("docs", { command: "node", args: ["-e", SERVER], exposure: "direct" });
}
