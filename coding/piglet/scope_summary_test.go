package piglet

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

func TestMCPScopeLineAndSummary(t *testing.T) {
	names := []string{"mcp__docs__a"}
	none := []string{}
	read := []string{"read"}
	only := []string{"x"}
	for _, test := range []struct {
		name    string
		piglet  *Piglet
		line    string
		summary string
	}{
		{"no extensions section", &Piglet{}, "", "all built-in tools; all MCP tools"},
		{"without the adapter", &Piglet{Extensions: []ExtensionEntry{{Name: "other"}}}, "MCP tools: hidden, because the Piglet lists `extensions:` but not `pig-mcp-adapter`", "all built-in tools; MCP tools hidden"},
		{"adapter without tools", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName}}}, "", "all built-in tools; all MCP tools"},
		{"adapter with a list", &Piglet{Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &names}}}, "MCP tools: only mcp__docs__a, because the `pig-mcp-adapter` entry's `tools` list does not name the others", "all built-in tools; MCP tools only mcp__docs__a"},
		{"adapter with an empty list", &Piglet{BuiltinTools: &read, Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &none}, {Name: "ext", Tools: &only}}}, "MCP tools: hidden, because the `pig-mcp-adapter` entry lists no tools", "built-in tools read; MCP tools hidden; tools of ext only x"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := MCPScopeLine(test.piglet); got != test.line {
				t.Errorf("MCPScopeLine = %q, want %q", got, test.line)
			}
			if got := ToolScopeSummary(test.piglet); got != test.summary {
				t.Errorf("ToolScopeSummary = %q, want %q", got, test.summary)
			}
		})
	}
}

// The notice groups the hidden tools by server or extension and gives each group its reason.
func TestToolScopeNoticeNamesServersToolsAndReasons(t *testing.T) {
	allow := []string{"mcp__docs__a"}
	p := &Piglet{Name: "demo", Extensions: []ExtensionEntry{{Name: MCPAdapterExtensionName, Tools: &allow}}}
	scope := CompileToolScope(p, nil)
	docs := &extension.ToolNamespace{Name: extension.McpNamespace("docs")}
	var hidden []extension.HiddenTool
	for _, tool := range []extension.ToolInfo{{Name: "mcp__docs__a", Namespace: docs}, {Name: "mcp__docs__b", Namespace: docs}, {Name: "mcp__docs__c", Namespace: docs}, {Name: "read_mcp_resource"}} {
		if info, ok := scope.HiddenInfo(tool); ok {
			hidden = append(hidden, info)
		}
	}
	want := "Piglet demo hid 3 tools from the registry:\n" +
		"- MCP server docs: mcp__docs__b, mcp__docs__c (the `pig-mcp-adapter` entry's `tools` list does not name it)\n" +
		"- MCP resource tools: read_mcp_resource (the `pig-mcp-adapter` entry's `tools` list does not name it)"
	if got := ToolScopeNotice(p, hidden); got != want {
		t.Errorf("notice =\n%s\nwant\n%s", got, want)
	}
	if got := ToolScopeNotice(p, nil); got != "" {
		t.Errorf("notice without hidden tools = %q", got)
	}
	if !strings.Contains(AgentToolScopeLine(p, false), "Active Piglet: demo.") {
		t.Errorf("agent line = %q", AgentToolScopeLine(p, false))
	}
}

// `pig piglet show` says why a Piglet hides MCP tools, and says nothing when it keeps them.
func TestShowPrintsTheMCPScopeLine(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	for name, test := range map[string]struct{ yaml, want string }{
		"hides": {"name: x\nextensions:\n  - name: other\n    tools: [t]\n", "MCP tools: hidden, because the Piglet lists `extensions:` but not `pig-mcp-adapter`"},
		"keeps": {"name: y\n", ""},
	} {
		path := dir + "/" + name + ".yaml"
		if err := os.WriteFile(path, []byte(test.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr strings.Builder
		if code := cmdShow([]string{path}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: show exit %d\n%s%s", name, code, stdout.String(), stderr.String())
		}
		if got := strings.Contains(stdout.String(), "MCP tools:"); got != (test.want != "") || test.want != "" && !strings.Contains(stdout.String(), test.want) {
			t.Errorf("%s: show output\n%s\nwant line %q", name, stdout.String(), test.want)
		}
	}
}

// The agent line states the most the agent can reach in this process: it leaves out what the strip lists remove.
func TestToolScopeSummaryLeavesOutStrippedTools(t *testing.T) {
	list := []string{"read", "bash"}
	undoTool := pigstrip.Strip(pigstrip.ListTools, "bash")
	defer undoTool()
	undoMCP := pigstrip.Strip(pigstrip.ListExtensions, "mcp")
	defer undoMCP()
	if got, want := ToolScopeSummary(&Piglet{BuiltinTools: &list}), "built-in tools read; no MCP tools"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got, want := ToolScopeSummary(&Piglet{}), "all built-in tools except bash; no MCP tools"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// The agent line names the Piglet, the most it can reach, what the strip lists removed and where the details are.
func TestAgentToolScopeLineNamesStripsAndDetails(t *testing.T) {
	undo := pigstrip.Strip(pigstrip.ListTools, "bash")
	defer undo()
	undoExt := pigstrip.Strip(pigstrip.ListExtensions, "mcp")
	defer undoExt()
	p := &Piglet{Name: "demo", sourcePath: "/p/demo.yaml"}
	want := "Active Piglet: demo. Tool scope, at most: all built-in tools except bash; no MCP tools. Stripped: tools bash; extensions mcp. Details: pig piglet show /p/demo.yaml."
	if got := AgentToolScopeLine(p, false); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	p.lineage = []LineageEntry{{Source: "binary:base"}}
	if got := AgentToolScopeLine(p, false); !strings.HasPrefix(got, "Active Piglet: demo (overlay /p/demo.yaml on Piglet Binary base). ") {
		t.Errorf("overlay line = %q", got)
	}
}

// An untrusted project is one short clause of the agent line, and only then.
func TestAgentToolScopeLineNamesAnUntrustedProject(t *testing.T) {
	p := &Piglet{Name: "demo"}
	if line := AgentToolScopeLine(p, false); strings.Contains(line, "Trust:") {
		t.Errorf("trusted line = %q", line)
	}
	if line := AgentToolScopeLine(p, true); !strings.Contains(line, " Trust: untrusted project (project resources not loaded). Details:") {
		t.Errorf("untrusted line = %q", line)
	}
}

// `tools` limits and does not activate: show prints what a run activates beside the limit.
func TestShowPrintsTheToolsActiveByDefault(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	path := t.TempDir() + "/r.yaml"
	if err := os.WriteFile(path, []byte("name: r\ntools: [read, grep, find, ls]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := cmdShow([]string{path}, &stdout, &stderr); code != 0 {
		t.Fatalf("show exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Built-in tools: read, grep, find, ls (a limit; it does not activate a tool)\n", "Active by default: read\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show output lacks %q:\n%s", want, stdout.String())
		}
	}
}
