package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A Piglet scopes the built-in MCP extension's tools as it scopes the pig-mcp-adapter's (additive feature D23,
// docs/design/builtin-mcp.md step 2): a positive-only Piglet that does not list the adapter entry hides every
// `mcp__<server>__<tool>` tool and the tools that reach servers (codemode, tool_search, the MCP resource tools), and an
// adapter entry brings them back, narrowed by its `tools` list. The first model request is the observation, in RPC mode,
// with a stub stdio MCP server and an isolated home and working directory.
func TestPigletScopesBuiltinMCPToolsInRPCMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, exposure := range []string{"direct", "codemode"} {
		for _, test := range []struct {
			name, entries string
			// want and absent are tools of the first request.
			want, absent []string
		}{
			{"a positive-only Piglet that omits the adapter hides the server", "", []string{"read", "scoped_tool"}, []string{"mcp__docs__search", "codemode", "tool_search", "list_mcp_resources", "read_mcp_resource"}},
			{"the adapter entry keeps the server", "  - name: pig-mcp-adapter\n", nil, nil},
			{"the adapter entry's tools list narrows the server", "  - name: pig-mcp-adapter\n    tools: [other_tool]\n", []string{"read", "scoped_tool"}, []string{"mcp__docs__search", "codemode", "tool_search", "list_mcp_resources", "read_mcp_resource"}},
		} {
			t.Run(exposure+"/"+test.name, func(t *testing.T) {
				root := t.TempDir()
				piglet := filepath.Join(root, "piglet")
				if err := os.MkdirAll(piglet, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(piglet, "scoped.mjs"), []byte(`export default function (pi) {
  pi.registerTool({ name: "scoped_tool", label: "Scoped", description: "scoped", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
}
`), 0o600); err != nil {
					t.Fatal(err)
				}
				yaml := "name: mcp-scope\nrelease:\n  version: 0.1.0\nextensions:\n  - name: scoped\n    origins: [local:./scoped.mjs]\n" + test.entries + "skills: []\ndiscovery:\n  extensions: []\n  skills: []\n"
				if err := os.WriteFile(filepath.Join(piglet, "piglet.yaml"), []byte(yaml), 0o600); err != nil {
					t.Fatal(err)
				}
				result := runPigWithMCP(t, mcpBinaryRun{
					mode:    "rpc",
					prompt:  "go",
					servers: []mcpBinaryServer{{name: "docs", config: map[string]any{"exposure": exposure}}},
					steps:   []chatStep{chatText("done")},
					args:    []string{"--piglet", filepath.Join(piglet, "piglet.yaml"), "--no-extensions", "-e", "builtin:mcp"},
				})
				requests := result.chat.recorded()
				if len(requests) == 0 {
					t.Fatalf("no model request\n%s", result.output)
				}
				tools := requests[0].Tools
				if test.want == nil {
					// The adapter entry keeps what the server exposes: the direct tool, or codemode for the codemode exposure.
					if exposure == "direct" {
						test.want = []string{"read", "scoped_tool", "mcp__docs__search"}
					} else {
						test.want = []string{"read", "scoped_tool", "codemode"}
					}
				}
				for _, name := range test.want {
					if !slices.Contains(tools, name) {
						t.Errorf("first request tools = %v, want %q", tools, name)
					}
				}
				for _, name := range test.absent {
					if slices.Contains(tools, name) {
						t.Errorf("first request tools = %v, want no %q", tools, name)
					}
				}
			})
		}
	}
}
