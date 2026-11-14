//go:build !pig_strip_mcp

package coding

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// mcpLoneSurrogateServer is an MCP server over stdio whose one tool returns a text with a lone UTF-16 surrogate. It writes its messages with
// JSON.stringify, as an MCP server on Node does, so the unit crosses the pipe as the escape \ud83d.
const mcpLoneSurrogateServer = `const lines = require("node:readline").createInterface({ input: process.stdin });
const send = (message) => process.stdout.write(JSON.stringify(message) + "\n");
lines.on("line", (line) => {
  const message = JSON.parse(line);
  if (message.id === undefined) return;
  const result = (value) => send({ jsonrpc: "2.0", id: message.id, result: value });
  if (message.method === "initialize") result({ protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "docs", version: "1.0.0" } });
  else if (message.method === "tools/list") result({ tools: [{ name: "lone", description: "Returns a lone surrogate.", inputSchema: { type: "object", properties: {} } }] });
  else if (message.method === "tools/call") result({ content: [{ type: "text", text: "MC[\ud83d]" }] });
  else result({});
});
`

// pi: the MCP client hands a tool result's text to the session as the string JSON.parse read (mcp/tools.ts convertMcpResult), so a lone UTF-16
// surrogate a server sent stays in the tool result message, like the result of any other tool; it is removed only where a provider request is built.
// PiG-additive path: the MCP client is PiG's own JSON-RPC reader.
func TestAgentSessionMCPToolResultKeepsALoneSurrogate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("the stdio MCP server fixture runs on node: %v", err)
	}
	script := filepath.Join(t.TempDir(), "server.cjs")
	if err := os.WriteFile(script, []byte(mcpLoneSurrogateServer), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []mcpext.McpServerEntry{{Name: "docs", Config: extension.McpServerConfig{Command: node, Args: []string{script}, Exposure: extension.McpExposureDirect}, Source: "test"}}
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{onlyMCP: true, realTransport: true, config: &entries, skipToolWait: true})
	mcpWaitFor(t, "mcp__docs__lone to be registered", func() bool { return h.hasTool("mcp__docs__lone") })
	h.respond(mcpToolCalls(mcpToolCall{"mcp__docs__lone", ai.JsonObject{}}), mcpDone())

	h.prompt(t, "go")

	const unit = "\xed\xa0\xbd"
	requireEqual(t, "result text", mcpText(h.toolResult(t, "mcp__docs__lone")), "MC["+unit+"]")
}
