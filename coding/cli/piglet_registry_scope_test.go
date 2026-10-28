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
	// noExtensionsSection writes a Piglet with no `extensions:` section. withoutCodemode leaves `-e builtin:codemode` and
	// `-e builtin:tool-search` off the command line.
	noExtensionsSection bool
	withoutCodemode     bool
	servers             []mcpBinaryServer
	steps               []chatStep
	args                []string
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
	extensions := "extensions:\n  - name: scoped\n    origins: [local:./scoped.mjs]\n" + run.scopedTools + run.entries
	if run.noExtensionsSection {
		extensions = ""
	}
	yaml := "name: registry-scope\nrelease:\n  version: 0.1.0\n" + extensions + "skills: []\ndiscovery:\n  extensions: []\n  skills: []\n"
	if err := os.WriteFile(filepath.Join(piglet, "piglet.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	launch := []string{"--piglet", filepath.Join(piglet, "piglet.yaml"), "--no-extensions", "-e", "builtin:mcp"}
	if !run.withoutCodemode {
		launch = append(launch, "-e", "builtin:codemode", "-e", "builtin:tool-search")
	}
	return runPigWithMCP(t, mcpBinaryRun{
		mode:    "rpc",
		prompt:  "go",
		servers: run.servers,
		steps:   run.steps,
		args:    append(launch, run.args...),
	})
}

// docsServer serves the tools a and b with the given exposure.
func docsServer(exposure string) mcpBinaryServer {
	return mcpBinaryServer{name: "docs", config: map[string]any{"exposure": exposure}, env: map[string]string{"MCP_FIXTURE_TOOLS": "a,b"}}
}

func codemodeCall(tool string) chatStep {
	return chatCall("codemode", map[string]any{"code": "return (await tools." + tool + `({ query: "q" })).content[0].text;`})
}

// codemodeCallJSON calls an extension tool and returns its whole result as JSON.
func codemodeCallJSON(tool string) chatStep {
	return chatCall("codemode", map[string]any{"code": "return JSON.stringify(await tools." + tool + "({}));"})
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
			args:    []string{"--tools", "read,codemode,tool_search"},
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
			args:    []string{"--tools", "read,codemode,tool_search"},
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
  // codemode and tool_search are registered inactive; the model needs them declared to reach the tools above.
  pi.on("session_start", async () => {
    pi.setActiveTools([...new Set([...pi.getActiveTools(), "codemode", "tool_search"])]);
  });
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
			steps: []chatStep{codemodeCallJSON("hidden_codemode"), codemodeCallJSON("shown_codemode"), chatText("done")},
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
			steps: []chatStep{chatCall("tool_search", map[string]any{"query": "deferred", "limit": 10}), chatText("done")},
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

// The five rules of the tool scope table, observed as the first model request of the real binary in RPC mode: the declared
// tools, the one line the agent gets about the Piglet, and the notice of the hidden tools.
func TestPigletToolScopeRulesInTheFirstRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, test := range []struct {
		name string
		run  pigletRegistryRun
		// declared and absent are tools of the first request; scopeLine is in its system prompt; notice is in the output, or empty when nothing is hidden.
		declared, absent []string
		scopeLine        string
		notice           string
	}{
		{"no extensions section", pigletRegistryRun{noExtensionsSection: true}, []string{"mcp__docs__a", "mcp__docs__b"}, nil, "all MCP tools", ""},
		{"extensions without the adapter", pigletRegistryRun{withoutCodemode: true}, nil, []string{"mcp__docs__a", "mcp__docs__b"}, "MCP tools hidden", "the Piglet lists `extensions:` but not `pig-mcp-adapter`"},
		{"the adapter without tools", pigletRegistryRun{entries: "  - name: pig-mcp-adapter\n"}, []string{"mcp__docs__a", "mcp__docs__b"}, nil, "all MCP tools", ""},
		{"the adapter with a tools list", pigletRegistryRun{entries: "  - name: pig-mcp-adapter\n    tools: [mcp__docs__a]\n"}, []string{"mcp__docs__a"}, []string{"mcp__docs__b"}, "MCP tools only mcp__docs__a", "mcp__docs__b"},
		{"builtin:codemode and builtin:tool-search selected", pigletRegistryRun{}, nil, []string{"mcp__docs__a", "mcp__docs__b"}, "MCP tools hidden", "the Piglet lists `extensions:` but not `pig-mcp-adapter`"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := test.run
			run.servers = []mcpBinaryServer{docsServer("direct")}
			run.steps = []chatStep{chatText("done")}
			result := runPigletRegistry(t, run)
			requests := result.chat.recorded()
			if len(requests) == 0 {
				t.Fatalf("no model request\n%s", result.output)
			}
			first := requests[0]
			for _, name := range test.declared {
				if !slices.Contains(first.Tools, name) {
					t.Errorf("first request tools = %v, want %q", first.Tools, name)
				}
			}
			for _, name := range test.absent {
				if slices.Contains(first.Tools, name) {
					t.Errorf("first request tools = %v, want no %q", first.Tools, name)
				}
			}
			if line := "Active Piglet: registry-scope. Tool scope, at most: all built-in tools; " + test.scopeLine; !strings.Contains(first.System, line) {
				t.Errorf("system prompt lacks %q", line)
			}
			if test.notice == "" {
				if strings.Contains(result.output, "hid ") && strings.Contains(result.output, "from the registry") {
					t.Errorf("output has a hidden-tools notice, want none\n%s", result.output)
				}
			} else if !strings.Contains(result.output, "Piglet registry-scope hid") || !strings.Contains(result.output, test.notice) {
				t.Errorf("output lacks the hidden-tools notice with %q\n%s", test.notice, result.output)
			}
		})
	}
}

// The stock rows of the rule table, without a Piglet: a bare `--tools` list keeps MCP tools registered for codemode and
// tool_search but does not offer them to the model, and an `mcp__` entry filters them.
func TestStockToolsFlagRowsOfTheScopeTable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	t.Run("--tools read offers no MCP tool", func(t *testing.T) {
		t.Parallel()
		result := runPigWithMCP(t, mcpBinaryRun{mode: "rpc", prompt: "go", servers: []mcpBinaryServer{docsServer("direct")}, steps: []chatStep{chatText("done")}, args: []string{"--tools", "read", "-e", "builtin:mcp"}})
		requests := result.chat.recorded()
		if len(requests) == 0 {
			t.Fatalf("no model request\n%s", result.output)
		}
		if tools := requests[0].Tools; slices.ContainsFunc(tools, func(name string) bool { return strings.HasPrefix(name, "mcp__docs__") }) || slices.Contains(tools, "codemode") {
			t.Errorf("first request tools = %v, want no MCP tool and no codemode", tools)
		}
	})
	t.Run("--tools with codemode keeps MCP tools reachable", func(t *testing.T) {
		t.Parallel()
		result := runPigWithMCP(t, mcpBinaryRun{mode: "rpc", prompt: "go", servers: []mcpBinaryServer{docsServer("codemode")},
			steps: []chatStep{codemodeCall("mcp__docs__a"), codemodeCall("mcp__docs__b"), chatText("done")},
			args:  []string{"--tools", "read,codemode", "-e", "builtin:mcp", "-e", "builtin:codemode"}})
		requests := result.chat.recorded()
		if len(requests) != 3 || !resultsMention(requests[1].Results, "q guide") || !resultsMention(requests[2].Results, "q guide") {
			t.Fatalf("requests = %d, results = %v\n%s", len(requests), requests, result.output)
		}
	})
	t.Run("--tools with an mcp__ entry filters MCP tools", func(t *testing.T) {
		t.Parallel()
		result := runPigWithMCP(t, mcpBinaryRun{mode: "rpc", prompt: "go", servers: []mcpBinaryServer{docsServer("codemode")},
			steps: []chatStep{codemodeCall("mcp__docs__b"), chatText("done")},
			args:  []string{"--tools", "read,codemode,mcp__docs__a", "-e", "builtin:mcp", "-e", "builtin:codemode"}})
		requests := result.chat.recorded()
		if len(requests) != 2 || !resultsMention(requests[1].Results, "mcp__docs__b does not exist") {
			t.Fatalf("requests = %d\n%s", len(requests), result.output)
		}
	})
}
