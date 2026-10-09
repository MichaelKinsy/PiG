package mcp

import (
	"encoding/json"
	"testing"
)

// packages/mcp/src/protocol/content.ts:79-111 blockToLlmContent reads a server's block as JavaScript does: an absent member is `undefined`, a member of
// another JSON type goes through String(), `"text" in resource` is key presence, and `resource.mimeType ?? "unknown type"` keeps an empty string.
// Every expectation below is what Pi's own toLlmContent returned for the block (node --experimental-strip-types over content.ts, Pi 1.1.0).
//
// This table is the Pi-probe evidence for the lenient decoding in content.go: the 25 blocks below were run through Pi 1.1.0's toLlmContent.
func TestToLLMContentReadsServerBlocksAsJavaScript(t *testing.T) {
	text := func(s string) LLMContent { return LLMContent{Type: LLMContentTypeText, Text: s} }
	for _, tc := range []struct {
		name  string
		block string
		want  LLMContent
	}{
		{"audio without a mimeType", `{"type":"audio","data":"d"}`, text("[audio undefined omitted]")},
		{"audio with a number mimeType", `{"type":"audio","data":"d","mimeType":5}`, text("[audio 5 omitted]")},
		{"audio with a null mimeType", `{"type":"audio","data":"d","mimeType":null}`, text("[audio null omitted]")},
		{"audio with an object mimeType", `{"type":"audio","data":"d","mimeType":{"a":1}}`, text("[audio [object Object] omitted]")},
		{"audio with an array mimeType", `{"type":"audio","data":"d","mimeType":[1,null,"x",[2,3]]}`, text("[audio 1,,x,2,3 omitted]")},
		{"audio with a boolean mimeType", `{"type":"audio","data":"d","mimeType":true}`, text("[audio true omitted]")},
		{"audio with a large number mimeType", `{"type":"audio","data":"d","mimeType":1.5e21}`, text("[audio 1.5e+21 omitted]")},
		{"link without a uri", `{"type":"resource_link","name":"n"}`, text("n: undefined")},
		{"link without a name", `{"type":"resource_link","uri":"u"}`, text("undefined: u")},
		{"link with a null name", `{"type":"resource_link","name":null,"uri":"u"}`, text("null: u")},
		{"link", `{"type":"resource_link","name":"n","uri":"u"}`, text("n: u")},
		{"binary resource without a mimeType", `{"type":"resource","resource":{"uri":"u","blob":"b"}}`, text("[binary resource u (unknown type) omitted]")},
		{"binary resource with an empty mimeType", `{"type":"resource","resource":{"uri":"u","mimeType":"","blob":"b"}}`, text("[binary resource u () omitted]")},
		{"binary resource", `{"type":"resource","resource":{"uri":"u","mimeType":"app/x","blob":"b"}}`, text("[binary resource u (app/x) omitted]")},
		{"binary resource without a uri", `{"type":"resource","resource":{"blob":"b"}}`, text("[binary resource undefined (unknown type) omitted]")},
		{"image resource", `{"type":"resource","resource":{"uri":"u","mimeType":"image/png","blob":"QUJD"}}`, LLMContent{Type: LLMContentTypeImage, Data: "QUJD", MimeType: "image/png"}},
		{"text resource", `{"type":"resource","resource":{"uri":"u","text":"hi"}}`, text("hi")},
		{"empty text resource", `{"type":"resource","resource":{"uri":"u","text":""}}`, text("")},
		{"resource with neither text nor blob", `{"type":"resource","resource":{"uri":"u"}}`, text("[binary resource u (unknown type) omitted]")},
		{"block without a type", `{"text":"x"}`, text("[unsupported MCP content undefined]")},
		{"block with a number type", `{"type":7}`, text("[unsupported MCP content 7]")},
		{"block with an unknown type", `{"type":"zzz"}`, text("[unsupported MCP content zzz]")},
		{"number block", `5`, text("[unsupported MCP content undefined]")},
		{"string block", `"s"`, text("[unsupported MCP content undefined]")},
		{"array block", `[1]`, text("[unsupported MCP content undefined]")},
	} {
		var result CallToolResult
		if err := json.Unmarshal([]byte(`{"content":[`+tc.block+`]}`), &result); err != nil {
			t.Fatalf("%s: a block is not validated upstream, got decode error %v", tc.name, err)
		}
		got := ToLLMContent(result)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: ToLLMContent = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
