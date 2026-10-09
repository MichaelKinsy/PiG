package mcp

import "testing"

// upstream: packages/mcp/src/client.ts:143-150 validateCallToolResult checks only that the result is an object, `content` (when
// present) an array and `structuredContent` (when present) an object; protocol/content.ts blockToLlmContent then reads whatever
// the blocks hold. A block member of another JSON type or a missing member never rejects the result, and a JavaScript template
// literal renders it: `${block.name}: ${block.uri}` is "undefined: undefined" for a bare resource_link.
func TestCallToolResultKeepsBlocksWithMissingOrIllTypedMembers(t *testing.T) {
	cases := []struct {
		name, result, want string
		wantError          bool
	}{
		{"resource_link without members", `{"content":[{"type":"resource_link"}]}`, "undefined: undefined", false},
		{"resource_link with a number and a boolean", `{"content":[{"type":"resource_link","name":5,"uri":true}]}`, "5: true", false},
		{"resource_link with null and an object", `{"content":[{"type":"resource_link","name":null,"uri":{"a":1}}]}`, "null: [object Object]", false},
		{"resource_link with an array", `{"content":[{"type":"resource_link","name":[1,[2,null],"x"],"uri":1.5e21}]}`, "1,2,,x: 1.5e+21", false},
		{"audio without a mime type", `{"content":[{"type":"audio"}]}`, "[audio undefined omitted]", false},
		{"block without a type", `{"content":[{"text":"x"}]}`, "[unsupported MCP content undefined]", false},
		{"block that is a string", `{"content":["str"]}`, "[unsupported MCP content undefined]", false},
		{"block with a type that is not a string", `{"content":[{"type":5}]}`, "[unsupported MCP content 5]", false},
		{"text that is a number", `{"content":[{"type":"text","text":5,"size":"1"}]}`, "5", false},
		{"truthy isError that is not a boolean", `{"content":[{"type":"text","text":"a"}],"isError":"x"}`, "a", true},
		{"falsy isError that is not a boolean", `{"content":[{"type":"text","text":"a"}],"isError":0}`, "a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := validateCallToolResult([]byte(c.result))
			if err != nil {
				t.Fatalf("validateCallToolResult rejected %s: %v", c.result, err)
			}
			content := ToLLMContent(*result)
			if len(content) != 1 || content[0].Text != c.want {
				t.Fatalf("ToLLMContent = %+v, want one text block %q", content, c.want)
			}
			if got := result.IsError != nil && *result.IsError; got != c.wantError {
				t.Fatalf("isError truthiness = %v, want %v", got, c.wantError)
			}
		})
	}
}
