package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/client.ts validateCallToolResult and packages/mcp/src/protocol/content.ts toLlmContent for a server that sends
// blocks with a missing or ill-typed member. The expected results are what Pi's toLlmContent returns for each block, read by running
// content.ts under Node (v24, --experimental-strip-types) on the same inputs.
func TestClientPassesLenientToolResultBlocksThroughLikePi(t *testing.T) {
	text := func(s string) []mcp.LLMContent { return []mcp.LLMContent{{Type: "text", Text: s}} }
	for _, tc := range []struct {
		name  string
		block string
		want  []mcp.LLMContent
	}{
		{"audio without mimeType", `{"type":"audio"}`, text("[audio undefined omitted]")},
		{"audio with an object mimeType", `{"type":"audio","mimeType":{"a":1}}`, text("[audio [object Object] omitted]")},
		{"audio with a number mimeType", `{"type":"audio","mimeType":1.50}`, text("[audio 1.5 omitted]")},
		{"resource_link without name, array uri", `{"type":"resource_link","uri":["a",null,2]}`, text("undefined: a,,2")},
		{"resource_link with a numeric size string", `{"type":"resource_link","name":"n","uri":"u","size":"1"}`, text("n: u")},
		{"resource without mimeType", `{"type":"resource","resource":{"uri":"u"}}`, text("[binary resource u (unknown type) omitted]")},
		{"resource with an empty mimeType", `{"type":"resource","resource":{"uri":"u","mimeType":""}}`, text("[binary resource u () omitted]")},
		{"resource with a null mimeType", `{"type":"resource","resource":{"uri":"u","mimeType":null}}`, text("[binary resource u (unknown type) omitted]")},
		{"resource image blob", `{"type":"resource","resource":{"blob":"b","mimeType":"image/png"}}`, []mcp.LLMContent{{Type: "image", Data: "b", MimeType: "image/png"}}},
		{"numeric type", `{"type":5}`, text("[unsupported MCP content 5]")},
		{"no type", `{}`, text("[unsupported MCP content undefined]")},
		{"array block", `[1]`, text("[unsupported MCP content undefined]")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := connect(t)
			server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
				return json.RawMessage(`{"content":[` + tc.block + `],"isError":"x"}`), nil
			})
			result, err := client.CallTool(t.Context(), "lenient", nil, mcp.RequestOptions{})
			if err != nil {
				t.Fatalf("a result whose block members have other JSON types is passed through: %v", err)
			}
			if result.IsError == nil || !*result.IsError {
				t.Fatalf(`isError "x" is truthy: %v`, result.IsError)
			}
			got := mcp.ToLLMContent(*result)
			if len(got) != len(tc.want) || got[0] != tc.want[0] {
				t.Fatalf("ToLLMContent = %#v, want %#v", got, tc.want)
			}
			_ = client.Close()
		})
	}
}

func TestToolResultIsErrorReadsAsJavaScriptTruthiness(t *testing.T) {
	// client.ts passes `isError` through and extensions/mcp/tools.ts tests `result.isError`.
	for raw, want := range map[string]bool{`true`: true, `false`: false, `0`: false, `1`: true, `""`: false, `"no"`: true, `[]`: true, `{}`: true} {
		var result mcp.CallToolResult
		if err := json.Unmarshal([]byte(`{"content":[],"isError":`+raw+`}`), &result); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if result.IsError == nil || *result.IsError != want {
			t.Errorf("isError %s = %v, want %v", raw, result.IsError, want)
		}
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[],"isError":null}`), &result); err != nil || result.IsError != nil {
		t.Errorf("a null isError is absent: %v %v", result.IsError, err)
	}
}
