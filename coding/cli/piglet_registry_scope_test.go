//go:build !pig_strip_mcp

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A Piglet's tool scope bounds the Session's tool registry, as `--tools` and `--exclude-tools` do (Pi's _isAllowedTool,
// agent-session.ts:3457-3521; pig additive D23). A tool the Piglet hides is not only inactive: codemode cannot call it and
// tool_search cannot find it. The observation is the real binary in RPC mode against a scripted model and a stub stdio
// MCP server, with an isolated home, agent directory, working directory and session directory.

// pigletRegistryRun writes a Piglet with the given YAML extension entries (after the `scoped` extension, which registers
// the tools of scopedExtension) and runs RPC mode with the codemode and tool_search built-ins.
type pigletRegistryRun struct {
	entries string
	// scopedExtension is the source of the `scoped` extension; empty registers no tools. scopedTools is the YAML after its
	// origins, such as a `tools` list.
	scopedExtension string
	scopedTools     string
	servers         []mcpBinaryServer
	steps           []chatStep
	args            []string
}

func runPigletRegistry(t *testing.T, run pigletRegistryRun) mcpBinaryResult {
	t.Helper()
	root := t.TempDir()
	piglet := filepath.Join(root, "piglet")
	if err := os.MkdirAll(piglet, 0o700); err != nil {
		t.Fatal(err)
	}
	source := run.scopedExtension
	if source == "" {
		source = "export default function () {}\n"
	}
	if err := os.WriteFile(filepath.Join(piglet, "scoped.mjs"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "name: registry-scope\nrelease:\n  version: 0.1.0\nextensions:\n  - name: scoped\n    origins: [local:./scoped.mjs]\n" + run.scopedTools + run.entries + "skills: []\ndiscovery:\n  extensions: []\n  skills: []\n"
	if err := os.WriteFile(filepath.Join(piglet, "piglet.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return runPigWithMCP(t, mcpBinaryRun{
		mode:    "rpc",
		prompt:  "go",
		servers: run.servers,
		steps:   run.steps,
		args:    append([]string{"--piglet", filepath.Join(piglet, "piglet.yaml"), "--no-extensions", "-e", "builtin:mcp", "-e", "builtin:codemode", "-e", "builtin:tool-search"}, run.args...),
	})
}

// docsServer serves the tools a and b with the given exposure.
func docsServer(exposure string) mcpBinaryServer {
	return mcpBinaryServer{name: "docs", config: map[string]any{"exposure": exposure}, env: map[string]string{"MCP_FIXTURE_TOOLS": "a,b"}}
}

func codemodeCall(tool string) chatStep {
	return chatCall("codemode", map[string]any{"code": "return (await tools." + tool + `({ query: "q" })).content[0].text;`})
}

// resultsMention reports whether a tool result of the request mentions text.
func resultsMention(results []string, text string) bool {
	return slices.ContainsFunc(results, func(result string) bool { return strings.Contains(result, text) })
}

// A Piglet that allows only mcp__docs__a leaves mcp__docs__b out of the registry: a codemode script that calls it fails as
// under `--tools read,mcp__docs__a`, the server never sees the call, and tool_search cannot find it.
func TestPigletPartialMCPAllowlistRemovesTheOtherToolsFromTheRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	const entries = "  - name: pig-mcp-adapter\n    tools: [mcp__docs__a]\n"
	t.Run("codemode", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			entries: entries,
			servers: []mcpBinaryServer{docsServer("codemode")},
			steps:   []chatStep{codemodeCall("mcp__docs__b"), codemodeCall("mcp__docs__a"), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) != 3 {
			t.Fatalf("requests = %d, want 3\n%s", len(requests), result.output)
		}
		if !resultsMention(requests[1].Results, "mcp__docs__b does not exist") {
			t.Errorf("the hidden tool's script result = %q, want %q", requests[1].Results, "mcp__docs__b does not exist")
		}
		if !resultsMention(requests[2].Results, "q guide") {
			t.Errorf("the allowed tool's script result = %q, want %q", requests[2].Results, "q guide")
		}
		if !slices.Equal(result.log, []string{`docs:a:{"query":"q"}`}) {
			t.Errorf("server calls = %q, want only the allowed tool", result.log)
		}
	})
	t.Run("tool_search", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			entries: entries,
			servers: []mcpBinaryServer{docsServer("deferred")},
			steps:   []chatStep{chatCall("tool_search", map[string]any{"query": "docs", "limit": 10}), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) != 2 {
			t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
		}
		if !resultsMention(requests[1].Results, "mcp__docs__a") {
			t.Errorf("tool_search results = %q, want the allowed tool", requests[1].Results)
		}
		if resultsMention(requests[1].Results, "mcp__docs__b") || slices.Contains(requests[1].Tools, "mcp__docs__b") {
			t.Errorf("tool_search results = %q, tools = %v, want no hidden tool", requests[1].Results, requests[1].Tools)
		}
	})
}

// A Piglet that hides every MCP tool leaves none for codemode or tool_search: the call fails and the server sees nothing.
func TestPigletHidingEveryMCPToolLeavesNoneForCodemodeAndToolSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	t.Run("codemode", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			servers: []mcpBinaryServer{docsServer("codemode")},
			steps:   []chatStep{codemodeCall("mcp__docs__a"), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) != 2 {
			t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
		}
		if !resultsMention(requests[1].Results, "mcp__docs__a does not exist") {
			t.Errorf("script result = %q, want %q", requests[1].Results, "mcp__docs__a does not exist")
		}
		if len(result.log) != 0 {
			t.Errorf("server calls = %q, want none", result.log)
		}
	})
	t.Run("tool_search", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			servers: []mcpBinaryServer{docsServer("deferred")},
			steps:   []chatStep{chatCall("tool_search", map[string]any{"query": "docs", "limit": 10}), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) < 2 {
			t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
		}
		if resultsMention(requests[1].Results, "mcp__docs__") || slices.ContainsFunc(requests[1].Tools, func(name string) bool { return strings.HasPrefix(name, "mcp__docs__") }) {
			t.Errorf("tool_search results = %q, tools = %v, want no MCP tool", requests[1].Results, requests[1].Tools)
		}
	})
}

// scopedToolsExtension registers three tools of the `scoped` extension for each exposure: the Piglet lists `shown_*` and
// hides `hidden_*`.
const scopedToolsExtension = `export default function (pi) {
  for (const exposure of ["codemode", "deferred"]) {
    for (const prefix of ["shown", "hidden"]) {
      const name = prefix + "_" + exposure;
      pi.registerTool({ name, label: name, description: "a " + name + " registry probe", exposure, parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ran " + name }] }) });
    }
  }
}
`

// The scope reaches an extension's tools that only codemode and tool_search see, not only MCP tools.
func TestPigletRemovesHiddenExtensionToolsFromTheRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	const scopedTools = "    tools: [shown_codemode, shown_deferred]\n"
	t.Run("codemode exposure", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			scopedExtension: scopedToolsExtension, scopedTools: scopedTools,
			steps: []chatStep{codemodeCall("hidden_codemode"), codemodeCall("shown_codemode"), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) != 3 {
			t.Fatalf("requests = %d, want 3\n%s", len(requests), result.output)
		}
		if !resultsMention(requests[1].Results, "hidden_codemode does not exist") {
			t.Errorf("the hidden tool's script result = %q, want %q", requests[1].Results, "hidden_codemode does not exist")
		}
		if !resultsMention(requests[2].Results, "ran shown_codemode") {
			t.Errorf("the listed tool's script result = %q, want %q", requests[2].Results, "ran shown_codemode")
		}
	})
	t.Run("deferred exposure", func(t *testing.T) {
		t.Parallel()
		result := runPigletRegistry(t, pigletRegistryRun{
			scopedExtension: scopedToolsExtension, scopedTools: scopedTools,
			steps: []chatStep{chatCall("tool_search", map[string]any{"query": "registry probe", "limit": 10}), chatText("done")},
		})
		requests := result.chat.recorded()
		if len(requests) != 2 {
			t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
		}
		if !resultsMention(requests[1].Results, "shown_deferred") {
			t.Errorf("tool_search results = %q, want the listed tool", requests[1].Results)
		}
		if resultsMention(requests[1].Results, "hidden_") || slices.ContainsFunc(requests[1].Tools, func(name string) bool { return strings.HasPrefix(name, "hidden_") }) {
			t.Errorf("tool_search results = %q, tools = %v, want no hidden tool", requests[1].Results, requests[1].Tools)
		}
	})
}

// `--tools` and a Piglet scope intersect: a tool must pass both to stay in the registry.
func TestPigletScopeComposesWithToolsFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, test := range []struct {
		name, entries string
		args          []string
		// hidden is the tool neither or one side removes; kept is the one both admit.
		hidden, kept string
	}{
		{"the Piglet narrows --tools", "  - name: pig-mcp-adapter\n    tools: [mcp__docs__a]\n", []string{"--tools", "read,codemode,mcp__docs__a,mcp__docs__b"}, "mcp__docs__b", "mcp__docs__a"},
		{"--tools narrows the Piglet", "  - name: pig-mcp-adapter\n    tools: [mcp__docs__a, mcp__docs__b]\n", []string{"--tools", "read,codemode,mcp__docs__a"}, "mcp__docs__b", "mcp__docs__a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := runPigletRegistry(t, pigletRegistryRun{
				entries: test.entries, args: test.args,
				servers: []mcpBinaryServer{docsServer("codemode")},
				steps:   []chatStep{codemodeCall(test.hidden), codemodeCall(test.kept), chatText("done")},
			})
			requests := result.chat.recorded()
			if len(requests) != 3 {
				t.Fatalf("requests = %d, want 3\n%s", len(requests), result.output)
			}
			if !resultsMention(requests[1].Results, test.hidden+" does not exist") {
				t.Errorf("the removed tool's script result = %q, want %q", requests[1].Results, test.hidden+" does not exist")
			}
			if !resultsMention(requests[2].Results, "q guide") {
				t.Errorf("the kept tool's script result = %q, want %q", requests[2].Results, "q guide")
			}
			if len(result.log) != 1 || !strings.HasPrefix(result.log[0], "docs:"+strings.TrimPrefix(test.kept, "mcp__docs__")+":") {
				t.Errorf("server calls = %q, want only the kept tool", result.log)
			}
		})
	}
}
