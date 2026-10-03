package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream execute.ts searchTools(): a limit is any positive integer and the ranker slices with it
// (matches.slice(0, limit)), so a limit beyond the int range returns every match.
func TestSearchToolsAcceptsALimitBeyondTheIntRange(t *testing.T) {
	tools := []extension.AgentTool{{Name: "read_file", Description: "Read a file."}, {Name: "read_dir", Description: "Read a directory."}}
	globals := discoveryGlobals(tools, map[string]string{}, nil)
	got, err := globals[0].Execute(context.Background(), json.RawMessage(`["read",{"limit":1e20}]`))
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]string
	if err := json.Unmarshal(got, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("searchTools = %s, want both tools", got)
	}
}

// upstream execute.ts: when ctx.executeTool rejects, the row keeps status "running" with no duration or error, and
// becomes "cancelled" when the script ends; only an error outcome marks it "error".
func TestARejectedNestedCallLeavesItsRowRunningUntilTheScriptEnds(t *testing.T) {
	stale := errors.New("This extension ctx is stale")
	base := extension.NewContext("", nil, func() error { return stale }, extension.ContextActions{})
	r := &run{toolCallID: "call", tc: extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{})}
	if _, err := r.nestedCall(extension.AgentTool{Name: "read"})(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, stale) {
		t.Fatalf("nested call on a stale context: %v, want the stale error", err)
	}
	calls := r.snapshot()
	if len(calls) != 1 || calls[0].Status != StatusRunning || calls[0].Error != "" || calls[0].DurationMs != nil {
		t.Fatalf("row after the rejection = %+v, want running", calls)
	}
	if final := r.finishCalls(); final[0].Status != StatusCancelled {
		t.Fatalf("row after the script = %+v, want cancelled", final)
	}
}

// discoveryFixture is the nested tools of two MCP servers and a plain tool, with the namespaces ctx.tools reports, as
// execute.ts createDiscoveryGlobals sees them.
func discoveryFixture() (tools []extension.AgentTool, tc *extension.ToolContext) {
	docs := &extension.ToolNamespace{Name: "mcp__docs", Description: "Search the product docs", Instructions: "Always search before reading."}
	radius := &extension.ToolNamespace{Name: "mcp__dev_radius", Description: "Developer radius"}
	infos := []extension.ToolInfo{
		{Name: "read", Description: "Read a file."},
		{Name: "mcp__docs__search", Description: "Search the docs.", Namespace: docs},
		{Name: "mcp__docs__fail", Description: "Always fails.", Namespace: docs},
		{Name: "mcp__dev_radius__search", Description: "Search the radius.", Namespace: radius},
		{Name: "mcp__dev_radius__fail", Description: "Fails the radius.", Namespace: radius},
	}
	for _, info := range infos {
		tools = append(tools, extension.AgentTool{Name: info.Name, Description: info.Description})
	}
	base := extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{GetAllTools: func() []extension.ToolInfo { return infos }})
	return tools, extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{})
}

func callGlobal(t *testing.T, globals []sandbox.Tool, name, args string) (json.RawMessage, error) {
	t.Helper()
	for _, global := range globals {
		if global.Name == name {
			return global.Execute(context.Background(), json.RawMessage(args))
		}
	}
	t.Fatalf("no global %s", name)
	return nil, nil
}

// agent-session-mcp.test.ts (0.99.2) "describes the server with its configured description and returns its
// instructions to scripts": describeNamespace(name) resolves to { name, description?, instructions?, tools } for a
// namespace of nested tools, or undefined; execute.ts createDiscoveryGlobals.
func TestDescribeNamespaceReturnsTheNamespaceItsInstructionsAndItsToolNames(t *testing.T) {
	tools, tc := discoveryFixture()
	globals := discoveryGlobals(tools, map[string]string{}, tc)
	want := `{"name":"mcp__docs","description":"Search the product docs","instructions":"Always search before reading.","tools":["mcp__docs__search","mcp__docs__fail"]}`
	for _, name := range []string{"mcp__docs", "docs"} {
		got, err := callGlobal(t, globals, "describeNamespace", `["`+name+`"]`)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqualValue(t, got, want) {
			t.Errorf("describeNamespace(%q) = %s, want %s", name, got, want)
		}
	}
	// A namespace without a description or instructions leaves the keys out.
	got, err := callGlobal(t, globals, "describeNamespace", `["mcp__dev_radius"]`)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqualValue(t, got, `{"name":"mcp__dev_radius","description":"Developer radius","tools":["mcp__dev_radius__search","mcp__dev_radius__fail"]}`) {
		t.Errorf("describeNamespace(mcp__dev_radius) = %s", got)
	}
	// An unknown namespace resolves to undefined.
	none, err := callGlobal(t, globals, "describeNamespace", `["mcp__nope"]`)
	if err != nil || (len(none) != 0 && string(none) != "null") {
		t.Errorf("describeNamespace(mcp__nope) = %s, %v, want undefined", none, err)
	}
	if _, err := callGlobal(t, globals, "describeNamespace", `[7]`); err == nil || err.Error() != "describeNamespace() expects a namespace name" {
		t.Errorf("describeNamespace(7) error = %v", err)
	}
}

// CHANGELOG 0.99.2: describeNamespace() and searchTools() accept a namespace as mcp__dev-radius, mcp__dev_radius,
// dev-radius, or dev_radius; execute.ts isNamespaceName.
func TestNamespaceNamesAcceptTheServerNameTheIdentifierAndTheirSuffixes(t *testing.T) {
	tools, tc := discoveryFixture()
	globals := discoveryGlobals(tools, map[string]string{}, tc)
	for _, name := range []string{"mcp__dev-radius", "mcp__dev_radius", "dev-radius", "dev_radius"} {
		got, err := callGlobal(t, globals, "describeNamespace", `["`+name+`"]`)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqualValue(t, got, `{"name":"mcp__dev_radius","description":"Developer radius","tools":["mcp__dev_radius__search","mcp__dev_radius__fail"]}`) {
			t.Errorf("describeNamespace(%q) = %s", name, got)
		}
		found, err := callGlobal(t, globals, "searchTools", `["search",{"namespace":"`+name+`"}]`)
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]string
		if err := json.Unmarshal(found, &entries); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry["name"])
		}
		slices.Sort(names)
		if !reflect.DeepEqual(names, []string{"mcp__dev_radius__search"}) {
			t.Errorf("searchTools(search, namespace %q) = %v, want the radius search tool", name, names)
		}
	}
	// Names that are neither the namespace nor a suffix of it match nothing.
	for _, name := range []string{"radius", "mcp__dev", "dev"} {
		none, err := callGlobal(t, globals, "describeNamespace", `["`+name+`"]`)
		if err != nil || (len(none) != 0 && string(none) != "null") {
			t.Errorf("describeNamespace(%q) = %s, %v, want undefined", name, none, err)
		}
	}
}

// createToolSearchDocument (0.99.2): a namespace's configured description and instructions are searchable, so
// searchTools() finds a server's tools by what the server offers.
func TestSearchToolsRanksToolsByTheirNamespacesInstructions(t *testing.T) {
	tools, tc := discoveryFixture()
	globals := discoveryGlobals(tools, map[string]string{}, tc)
	found, err := callGlobal(t, globals, "searchTools", `["always reading"]`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(found), "mcp__docs__search") || strings.Contains(string(found), "dev_radius") {
		t.Errorf("searchTools = %s, want the docs tools", found)
	}
}

func jsonEqualValue(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(g, w)
}
