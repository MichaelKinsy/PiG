//go:build !pig_strip_bedrock_converse_stream && !pig_strip_changelog && !pig_strip_codemode && !pig_strip_docs && !pig_strip_export_html && !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_lisp && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust && !pig_strip_google_vertex && !pig_strip_llama_cpp && !pig_strip_mcp && !pig_strip_mermaid && !pig_strip_mistral_conversations && !pig_strip_node_extensions && !pig_strip_pig_login && !pig_strip_piglet_builder && !pig_strip_self_update && !pig_strip_syntax_highlight && !pig_strip_tool_search && !pig_strip_word_dictionaries

package piglet

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// These two tests strip tools and extensions in the process and compare the agent line with the text of a process that strips nothing
// else; a Piglet Binary built with a strip tag lists its compiled-out built-ins on the line too, so they run in a build without tags.

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
