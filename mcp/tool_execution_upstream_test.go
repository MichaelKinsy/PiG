package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Tool.execution is `{ taskSupport?: "forbidden" | "optional" | "required" }` (packages/mcp/src/protocol/types.ts:72-74, :83). The client hands
// a listed tool's execution to the host exactly as the server sent it: each of the three taskSupport values, an empty execution object (no
// taskSupport) and no execution at all stay distinct, and a tool re-encoded for a host keeps `execution` only when it was present.
// mutation-checked: dropping the `execution` json tag of Tool, renaming the `taskSupport` tag, or giving Execution a non-pointer type fails it
func TestListToolsKeepsEachToolExecutionTaskSupport(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) {
		tool := func(name string, execution any) map[string]any {
			entry := map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}}
			if execution != nil {
				entry["execution"] = execution
			}
			return entry
		}
		return map[string]any{"tools": []any{
			tool("forbid", map[string]any{"taskSupport": "forbidden"}),
			tool("opt", map[string]any{"taskSupport": "optional"}),
			tool("req", map[string]any{"taskSupport": "required"}),
			tool("empty", map[string]any{}),
			tool("none", nil),
		}}, nil
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 {
		t.Fatalf("tools = %d, want 5", len(tools))
	}
	want := map[string]*string{"forbid": new("forbidden"), "opt": new("optional"), "req": new("required"), "empty": new(""), "none": nil}
	for _, tool := range tools {
		expected := want[tool.Name]
		switch {
		case expected == nil && tool.Execution != nil:
			t.Fatalf("%s: execution = %+v, want none", tool.Name, tool.Execution)
		case expected != nil && (tool.Execution == nil || tool.Execution.TaskSupport != *expected):
			t.Fatalf("%s: execution = %+v, want taskSupport %q", tool.Name, tool.Execution, *expected)
		}
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	var back []map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range back {
		var name string
		_ = json.Unmarshal(entry["name"], &name)
		got[name] = string(entry["execution"])
	}
	for name, wantJSON := range map[string]string{"forbid": `{"taskSupport":"forbidden"}`, "opt": `{"taskSupport":"optional"}`, "req": `{"taskSupport":"required"}`, "empty": `{}`, "none": ""} {
		if got[name] != wantJSON {
			t.Fatalf("%s: re-encoded execution = %q, want %q", name, got[name], wantJSON)
		}
	}
}
