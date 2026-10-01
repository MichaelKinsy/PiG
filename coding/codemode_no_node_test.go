package coding

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// docs/specs/builtin-codemode-tool-search.md, "Verification": MCP tools run through codemode with no `node` on PATH. The tool
// is shaped like an MCP tool (`mcp__<server>__<tool>`, an output schema that is a CallToolResult, structured content), the
// script runs in QuickJS on wazero, and the whole path is Go: the extension, the sandbox and the nested call.

const mcpResultSchema = `{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"structuredContent":{"type":"object","properties":{"hits":{"type":"array","items":{"type":"string"}}},"required":["hits"]},"isError":{"type":"boolean"},"_meta":{"type":"object"}},"required":["content"]}`

func mcpStyleExtension() extension.Extension {
	definition := extension.ToolDefinition{
		Name: "mcp__docs__search", Label: "search", Description: "Search the docs.",
		Parameters:   json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		OutputSchema: json.RawMessage(mcpResultSchema),
		// MCP tools that are only reachable from scripts have `codemode` exposure (docs/specs/builtin-codemode-tool-search.md).
		Exposure: extension.ToolExposureCodemode,
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			var input struct{ Query string }
			_ = json.Unmarshal([]byte(strings.ToLower(string(params))), &input)
			structured := `{"content":[{"type":"text","text":"2 hits"}],"structuredContent":{"hits":["a:` + input.Query + `","b:` + input.Query + `"]},"isError":false}`
			return agent.AgentToolResult{
				Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: "2 hits"}},
				Details:           map[string]any{},
				StructuredContent: json.RawMessage(structured),
			}, nil
		},
	}
	return extension.Extension{Name: "docs", Path: "<inline:docs>", ResolvedPath: "<inline:docs>",
		Tools: map[string]extension.RegisteredTool{definition.Name: {Definition: definition}}, ToolOrder: []string{definition.Name}}
}

func TestCodemodeRunsMCPStyleToolsWithoutNodeOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if path, err := exec.LookPath("node"); err == nil {
		t.Fatalf("node is on PATH: %s", path)
	}
	h := newCodemodeHarness(t, codemodeHarnessOptions{goExtensions: []extension.Extension{mcpStyleExtension()}, activeTools: []string{"codemode"}})
	var description string
	for _, tool := range h.session.Tools() {
		if tool.Name() == "codemode" {
			description = tool.Schema().Description
		}
	}
	if !strings.Contains(description, "### `mcp__docs__search`") || !strings.Contains(description, "Promise<CallToolResult<{ hits: Array<string>; }>>") || !strings.Contains(description, "Shared MCP Types") {
		t.Errorf("the description does not declare the MCP tool as a CallToolResult:\n%s", description)
	}
	result := codemodeRun(t, h, `
		const result = await tools.mcp__docs__search({ query: "wazero" });
		return { hits: result.structuredContent.hits, isError: result.isError };`)
	if result.IsError {
		t.Fatalf("script failed: %s", codemodeResultText(t, result))
	}
	if got, want := codemodeResultText(t, result), `{"hits":["a:wazero","b:wazero"],"isError":false}`; got != want {
		t.Errorf("result = %q, want %q", got, want)
	}
	details := codemodeDetailsOf(t, result)
	if len(details.Calls) != 1 || details.Calls[0].Name != "mcp__docs__search" || details.Calls[0].Status != "ok" {
		t.Errorf("calls = %+v", details.Calls)
	}
}

// A timeout ends the script and cancels the nested call that is still running: the tool sees its context cancelled, the
// call row is `cancelled`, and the result comes back after the tool returned (docs/specs/builtin-codemode-tool-search.md,
// "Execution lifetime and cancellation").
func TestCodemodeTimeoutCancelsARunningNestedCall(t *testing.T) {
	sawCancel := make(chan struct{})
	definition := extension.ToolDefinition{
		Name: "slow", Label: "slow", Description: "Waits for cancellation.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		Exposure: extension.ToolExposureCodemode,
		Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			<-ctx.Done()
			close(sawCancel)
			return nil, ctx.Err()
		},
	}
	ext := extension.Extension{Name: "slow", Path: "<inline:slow>", ResolvedPath: "<inline:slow>",
		Tools: map[string]extension.RegisteredTool{"slow": {Definition: definition}}, ToolOrder: []string{"slow"}}
	h := newCodemodeHarness(t, codemodeHarnessOptions{goExtensions: []extension.Extension{ext}, activeTools: []string{"codemode"}})
	done := make(chan agent.ToolResultMessage, 1)
	go func() { done <- codemodeRun(t, h, "// @options: {\"timeout_ms\": 3000}\nawait tools.slow({});") }()
	var result agent.ToolResultMessage
	select {
	case result = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the script did not end: the nested call was not cancelled")
	}
	select {
	case <-sawCancel:
	default:
		t.Error("the result came back before the nested tool saw its cancellation")
	}
	if !result.IsError || !strings.Contains(codemodeResultText(t, result), "Script timed out") {
		t.Errorf("result = %q (isError %v)", codemodeResultText(t, result), result.IsError)
	}
	if calls := codemodeDetailsOf(t, result).Calls; len(calls) != 1 || calls[0].Status != "cancelled" {
		t.Errorf("calls = %+v, want one cancelled call", calls)
	}
}
