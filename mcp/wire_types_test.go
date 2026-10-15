package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// The MCP schema (packages/mcp/src/protocol/types.ts) lists every optional member below. A server may send each of them and the
// client keeps what it sent: ListToolsResult (protocol/types.ts:87), Tool (protocol/types.ts:76) and ToolExecution
// (protocol/types.ts:72).
func TestListToolsResultKeepsEveryToolMember(t *testing.T) {
	var result mcp.ListToolsResult
	err := json.Unmarshal([]byte(`{"tools":[{"name":"t","title":"T","description":"d","inputSchema":{"type":"object"},"outputSchema":{"type":"string"},"annotations":{"readOnlyHint":true},"execution":{"taskSupport":"optional"},"_meta":{"k":1}}],"nextCursor":"c2","_meta":{"page":2}}`), &result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.NextCursor != "c2" || string(result.Meta) != `{"page":2}` {
		t.Fatalf("result = %+v", result)
	}
	tool := result.Tools[0]
	if tool.Execution == nil || tool.Execution.TaskSupport != "optional" {
		t.Fatalf("execution = %+v, want taskSupport optional", tool.Execution)
	}
	if string(tool.Meta) != `{"k":1}` || tool.Title != "T" || string(tool.OutputSchema) != `{"type":"string"}` {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
		t.Fatalf("annotations = %+v", tool.Annotations)
	}
}

// Resource (packages/mcp/src/protocol/types.ts:94), ResourceTemplate (protocol/types.ts:106), ListResourcesResult
// (protocol/types.ts:116), ListResourceTemplatesResult (protocol/types.ts:122), ReadResourceResult (protocol/types.ts:128) and
// their ContentAnnotations (protocol/content.ts:1) keep every optional member a server sends.
func TestResourceResultsKeepEveryOptionalMember(t *testing.T) {
	var resources mcp.ListResourcesResult
	if err := json.Unmarshal([]byte(`{"resources":[{"uri":"file:///a","name":"a","title":"A","description":"da","mimeType":"text/plain","size":12.5,"annotations":{"audience":["user","assistant"],"priority":0.5,"lastModified":"2025-01-02T03:04:05Z"},"_meta":{"r":1}}],"nextCursor":"n","_meta":{"p":1}}`), &resources); err != nil {
		t.Fatal(err)
	}
	r := resources.Resources[0]
	if r.Title != "A" || r.Description != "da" || r.Size == nil || *r.Size != 12.5 || string(r.Meta) != `{"r":1}` || string(resources.Meta) != `{"p":1}` {
		t.Fatalf("resource = %+v", r)
	}
	a := r.Annotations
	if a == nil || len(a.Audience) != 2 || a.Audience[1] != "assistant" || a.Priority == nil || *a.Priority != 0.5 || a.LastModified != "2025-01-02T03:04:05Z" {
		t.Fatalf("annotations = %+v", a)
	}

	var templates mcp.ListResourceTemplatesResult
	if err := json.Unmarshal([]byte(`{"resourceTemplates":[{"uriTemplate":"file:///{p}","name":"t","title":"T","description":"dt","mimeType":"text/x","annotations":{"priority":1},"_meta":{"t":1}}],"_meta":{"q":1}}`), &templates); err != nil {
		t.Fatal(err)
	}
	tpl := templates.ResourceTemplates[0]
	if tpl.Title != "T" || tpl.Description != "dt" || tpl.Annotations == nil || *tpl.Annotations.Priority != 1 || string(tpl.Meta) != `{"t":1}` || string(templates.Meta) != `{"q":1}` {
		t.Fatalf("template = %+v", tpl)
	}

	var read mcp.ReadResourceResult
	if err := json.Unmarshal([]byte(`{"contents":[{"uri":"file:///a","text":"x"}],"_meta":{"z":1}}`), &read); err != nil {
		t.Fatal(err)
	}
	if string(read.Meta) != `{"z":1}` || len(read.Contents) != 1 {
		t.Fatalf("read = %+v", read)
	}
}

// CallToolResult keeps the _meta a server sends (packages/mcp/src/protocol/content.ts:65).
func TestCallToolResultKeepsMeta(t *testing.T) {
	var result mcp.CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[],"_meta":{"progressToken":"p"}}`), &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Meta) != `{"progressToken":"p"}` {
		t.Fatalf("meta = %s", result.Meta)
	}
}

// ServerCapabilities keeps every capability a server declares (packages/mcp/src/protocol/types.ts:30).
func TestServerCapabilitiesKeepEveryCapability(t *testing.T) {
	var caps mcp.ServerCapabilities
	if err := json.Unmarshal([]byte(`{"experimental":{"x":{}},"logging":{},"prompts":{"listChanged":true},"resources":{"subscribe":true,"listChanged":true},"tools":{"listChanged":false},"completions":{}}`), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Experimental["x"] == nil || caps.Logging == nil || caps.Completions == nil {
		t.Fatalf("object capabilities = %+v", caps)
	}
	if caps.Prompts == nil || caps.Prompts.ListChanged == nil || !*caps.Prompts.ListChanged || caps.Tools == nil || caps.Tools.ListChanged == nil || *caps.Tools.ListChanged || caps.Resources == nil || caps.Resources.Subscribe == nil || !*caps.Resources.Subscribe {
		t.Fatalf("capabilities = %+v", caps)
	}
}

// Pi's client sends { requestId, reason } in that order and omits an empty reason.
func TestCancelledNotificationEncodesRequestIDThenReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   mcp.CancelledNotification
		want string
	}{
		{"number id with reason", mcp.CancelledNotification{RequestID: mcp.NumberID(7), Reason: "stop"}, `{"requestId":7,"reason":"stop"}`},
		{"string id without reason", mcp.CancelledNotification{RequestID: mcp.StringID("a")}, `{"requestId":"a"}`},
	} {
		raw, err := json.Marshal(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(raw) != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, raw, tc.want)
		}
		var back mcp.CancelledNotification
		if err := json.Unmarshal(raw, &back); err != nil || back != tc.in {
			t.Errorf("%s: round trip %+v err %v, want %+v", tc.name, back, err, tc.in)
		}
	}
}
