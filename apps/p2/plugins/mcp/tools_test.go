package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// Ports test/p2/mcp-tools.test.ts: MCP tools as Durable tools: pi's names, with the hash suffix for every colliding name
// whatever the order, pi's exposures (code mode only by default, `tool_search` without code mode) with the `mcp_servers`
// section, structured output only for tools with an output schema (error results included), and error results with text.

func mcpTool(name string, outputSchema ...string) pimcp.Tool {
	tool := pimcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}
	if len(outputSchema) > 0 {
		tool.OutputSchema = json.RawMessage(outputSchema[0])
	}
	return tool
}

func noCall(context.Context, McpCallRequest) (*pimcp.CallToolResult, error) {
	return &pimcp.CallToolResult{Content: []pimcp.ContentBlock{}}, nil
}

// offerOf is what the worker offers for tools: their servers, reached from code mode unless reach says otherwise.
func offerOf(tools []ServerTool, reach ...McpReach) McpOffer {
	servers := []string{}
	for _, entry := range tools {
		found := false
		for _, known := range servers {
			found = found || known == entry.Server
		}
		if !found {
			servers = append(servers, entry.Server)
		}
	}
	for i := range servers {
		for j := i + 1; j < len(servers); j++ {
			if servers[j] < servers[i] {
				servers[i], servers[j] = servers[j], servers[i]
			}
		}
	}
	chosen := ReachCodemode
	if len(reach) > 0 {
		chosen = reach[0]
	}
	return McpOffer{Servers: servers, SignIn: []string{}, Tools: tools, Reach: chosen}
}

func namesOf(tools []ServerTool) []string {
	var names []string
	for _, registration := range CreateMcpExtension(offerOf(tools), noCall).Tools {
		names = append(names, registration.Name)
	}
	return names
}

func reversed(tools []ServerTool) []ServerTool {
	out := make([]ServerTool, len(tools))
	for i, tool := range tools {
		out[len(tools)-1-i] = tool
	}
	return out
}

func TestToolNamesPisAndEveryToolOfACollidingNameGetsTheHashSuffixWhateverTheOrder(t *testing.T) {
	plain := []ServerTool{{"docs", mcpTool("search")}, {"docs", mcpTool("a-b")}, {"docs", mcpTool("a_b")}}
	names := namesOf(plain)
	search, dash, underscore := names[0], names[1], names[2]
	suffixed := regexp.MustCompile(`^mcp__docs__a_b_[0-9a-f]{8}$`)
	if search != "mcp__docs__search" || !suffixed.MatchString(dash) || !suffixed.MatchString(underscore) || dash == underscore {
		t.Fatalf("names %v", names)
	}
	// The other order gives each tool the same name.
	if got := namesOf(reversed(plain)); !reflect.DeepEqual(got, []string{underscore, dash, search}) {
		t.Fatalf("reversed: %v", got)
	}
	// A new tool that collides with nothing renames nothing; one that collides renames only its rival.
	if got := namesOf(append(append([]ServerTool{}, plain...), ServerTool{"docs", mcpTool("fetch")})); !reflect.DeepEqual(got, []string{search, dash, underscore, "mcp__docs__fetch"}) {
		t.Fatalf("added: %v", got)
	}
	if got := namesOf([]ServerTool{{"docs", mcpTool("x-y")}})[0]; got != "mcp__docs__x_y" {
		t.Fatalf("alone: %s", got)
	}
	rivals := namesOf([]ServerTool{{"docs", mcpTool("search")}, {"docs", mcpTool("x-y")}, {"docs", mcpTool("x.y")}})
	suffixedXY := regexp.MustCompile(`^mcp__docs__x_y_[0-9a-f]{8}$`)
	if rivals[0] != "mcp__docs__search" || !suffixedXY.MatchString(rivals[1]) || !suffixedXY.MatchString(rivals[2]) {
		t.Fatalf("rivals: %v", rivals)
	}
}

func TestToolNamesACollisionAcrossServersSuffixesBothToolsToo(t *testing.T) {
	names := namesOf([]ServerTool{{"a", mcpTool("b__c")}, {"a__b", mcpTool("c")}})
	suffixed := regexp.MustCompile(`^mcp__a__b__c_[0-9a-f]{8}$`)
	if !suffixed.MatchString(names[0]) || !suffixed.MatchString(names[1]) || names[0] == names[1] {
		t.Fatalf("names %v", names)
	}
	if got := namesOf([]ServerTool{{"a", mcpTool("b__c")}})[0]; got != "mcp__a__b__c" {
		t.Fatalf("alone: %s", got)
	}
}

func TestExposureAsPisDefaultCodeModeOnlyDeferredSoTheCodemodeSectionLeavesTheToolsOut(t *testing.T) {
	tools := []ServerTool{{"docs", mcpTool("search")}}
	scripted := CreateMcpExtension(offerOf(tools), noCall).Tools[0]
	if !scripted.Deferred || !reflect.DeepEqual(scripted.Callers, []durable.ToolCaller{durable.CallerTools}) {
		t.Fatalf("codemode: deferred %v callers %v", scripted.Deferred, scripted.Callers)
	}
	// Without code mode (pi's fallback), `tool_search` loads them for the model.
	loaded := CreateMcpExtension(offerOf(tools, ReachToolSearch), noCall).Tools[0]
	if !loaded.Deferred || loaded.Callers != nil {
		t.Fatalf("tool_search: deferred %v callers %v", loaded.Deferred, loaded.Callers)
	}
	if scripted.Origin != "owner" {
		t.Fatalf("origin %q", scripted.Origin)
	}
}

func renderSection(t *testing.T, extension McpOffer) string {
	t.Helper()
	sections := CreateMcpExtension(extension, noCall).Extension.Sections
	if len(sections) == 0 || sections[0].Key != McpServersSection {
		t.Fatalf("sections %v", sections)
	}
	// The section renders a constant: it reads nothing of its input.
	text, err := sections[0].Render(context.Background(), durable.PromptInput{})
	if err != nil || text == nil {
		t.Fatalf("render: %v %v", text, err)
	}
	return *text
}

func TestTheMcpServersSectionNamesEachServersNamespaceAndHowItsToolsAreReachedAsPis(t *testing.T) {
	tools := []ServerTool{{"my-docs", mcpTool("search")}, {"app", mcpTool("read")}}
	if got, want := renderSection(t, offerOf(tools)),
		"MCP servers whose tools are not declared to you. Call the tools of `codemode` servers from codemode scripts.\n- mcp__app (codemode)\n- mcp__my_docs (codemode)"; got != want {
		t.Fatalf("got %q", got)
	}
	if got, want := renderSection(t, offerOf(tools, ReachToolSearch)),
		"MCP servers whose tools are not declared to you. Load the tools of `tool_search` servers with `tool_search`.\n- mcp__app (tool_search)\n- mcp__my_docs (tool_search)"; got != want {
		t.Fatalf("got %q", got)
	}
	// A server still connecting has no tools yet, and is listed; without servers there is no section.
	starting := McpOffer{Servers: []string{"slow"}, SignIn: []string{}, Reach: ReachCodemode}
	if got := renderSection(t, starting); !strings.HasSuffix(got, "- mcp__slow (codemode)") {
		t.Fatalf("got %q", got)
	}
	// A server waiting for a sign-in is listed with the link the user signs in with.
	locked := McpOffer{Servers: []string{"my-docs"}, SignIn: []string{"my-docs"}, Reach: ReachCodemode}
	if got := renderSection(t, locked); !regexp.MustCompile(`- mcp__my_docs \(codemode; needs a sign-in, no tools yet: the user signs in from the MCP panel in p2's sidebar or at /mcp/sign-in/my-docs on p2\)$`).MatchString(got) {
		t.Fatalf("got %q", got)
	}
	if sections := CreateMcpExtension(offerOf(nil), noCall).Extension.Sections; len(sections) != 0 {
		t.Fatalf("sections %v", sections)
	}
}

// stubAPI is the tool execution API as the tools read it: only the conversation ID.
type stubAPI struct{ durable.ToolExecutionApi }

func (stubAPI) ConversationId() durable.ConversationId { return 1 }

// registration is the registration of tool calling a server that answers result, and what it was called with.
type registered struct {
	tool  *durable.ToolRegistration
	calls *[]McpCallRequest
}

func registrationOf(tool pimcp.Tool, result pimcp.CallToolResult) registered {
	calls := []McpCallRequest{}
	call := func(_ context.Context, request McpCallRequest) (*pimcp.CallToolResult, error) {
		calls = append(calls, request)
		return &result, nil
	}
	extension := CreateMcpExtension(offerOf([]ServerTool{{"docs", tool}}), call)
	return registered{tool: extension.Tools[0].ToolRegistration, calls: &calls}
}

func (r registered) run(t *testing.T) durable.ToolExecutionResult {
	t.Helper()
	result, err := r.tool.Execute(context.Background(), map[string]any{"q": "x"}, stubAPI{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func textBlock(text string) pimcp.ContentBlock { return pimcp.ContentBlock{Type: "text", Text: text} }

func TestResultsStructuredOutputOnlyWithAnOutputSchemaErrorResultsIncludedErrorsAlwaysHaveText(t *testing.T) {
	schema := `{"type":"object","properties":{"hits":{"type":"number"}}}`
	structured := pimcp.CallToolResult{Content: []pimcp.ContentBlock{textBlock("2 hits")}, StructuredContent: json.RawMessage(`{"hits":2}`)}
	withSchema := registrationOf(mcpTool("search", schema), structured)
	var want map[string]any
	_ = json.Unmarshal([]byte(schema), &want)
	if !reflect.DeepEqual(withSchema.tool.StructuredOutputSchema, want) {
		t.Fatalf("schema %v", withSchema.tool.StructuredOutputSchema)
	}
	got := withSchema.run(t)
	if !reflect.DeepEqual(got.Output, []ai.ToolResultMessageContent{ai.TextContent{Text: "2 hits"}}) || got.IsError == nil || *got.IsError ||
		!reflect.DeepEqual(got.Details, map[string]any{"server": "docs", "tool": "search"}) ||
		!got.HasStructuredOutput || !reflect.DeepEqual(got.StructuredOutput, map[string]any{"hits": float64(2)}) {
		t.Fatalf("result %+v", got)
	}
	calls := *withSchema.calls
	if !reflect.DeepEqual(calls[0].Args, map[string]any{"q": "x"}) || calls[0].Tool != "search" {
		t.Fatalf("calls %+v", calls)
	}
	withoutSchema := registrationOf(mcpTool("search"), structured)
	if withoutSchema.tool.StructuredOutputSchema != nil || withoutSchema.run(t).HasStructuredOutput {
		t.Fatal("no schema: no structured output")
	}
	isError := true
	failed := registrationOf(mcpTool("search", schema), pimcp.CallToolResult{Content: []pimcp.ContentBlock{}, StructuredContent: json.RawMessage(`{"hits":0}`), IsError: &isError})
	failure := failed.run(t)
	if failure.IsError == nil || !*failure.IsError || !reflect.DeepEqual(failure.StructuredOutput, map[string]any{"hits": float64(0)}) {
		t.Fatalf("failure %+v", failure)
	}
	// Without content the structured content becomes the text; without either, an error still says so.
	encoded, _ := json.Marshal(failure.Output)
	if !strings.Contains(string(encoded), "hits") {
		t.Fatalf("output %s", encoded)
	}
	bare := registrationOf(mcpTool("search"), pimcp.CallToolResult{Content: []pimcp.ContentBlock{}, IsError: &isError}).run(t)
	if !reflect.DeepEqual(bare.Output, []ai.ToolResultMessageContent{ai.TextContent{Text: "MCP tool docs/search returned an error"}}) {
		t.Fatalf("bare %+v", bare.Output)
	}
}
