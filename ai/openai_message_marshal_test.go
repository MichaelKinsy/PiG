package ai

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The request encoder writes each message through MarshalJSONTo; its bytes must be the bytes MarshalJSON gives, under encoding/json's own options: HTML and U+2028 escaping, invalid UTF-8 replaced, raw reasoning details compacted, nil and empty fields as omitempty decides.
func TestOpenAIMessageEncodesAsItsMarshalJSON(t *testing.T) {
	reasoning := "r <b>&amp;</b>"
	messages := []oaiMessage{
		{Role: "system", Content: "You are <helpful> & \u2028 fast"},
		{Role: "user", Content: []oaiContentPart{{Type: "text", Text: new("hi \xed\xa0\xbd lone")}, {Type: "image_url", ImageURL: &struct {
			URL string `json:"url"`
		}{URL: "data:image/png;base64,AAAA"}}, {Type: "text", Text: new("cached"), CacheControl: &oaiCacheControl{Type: "ephemeral", TTL: "1h"}}}},
		{Role: "assistant", Content: nil, ToolCalls: []oaiRequestToolCall{{ID: "call_1", Type: "function", Function: &oaiRequestToolCallFunction{Name: "read", Arguments: `{"path":"a<b>.go"}`}}}, ReasoningDetails: []json.RawMessage{json.RawMessage(`{ "type" : "reasoning.text", "text":"x" }`)}, ReasoningContent: &reasoning},
		{Role: "tool", Content: "file \x00 body\n", ToolCallID: "call_1", ToolName: "read"},
		{Role: "assistant", Content: "", Reasoning: "think", ReasoningText: "text"},
		{Role: "assistant", Content: []oaiContentPart{}},
		{Role: "system", Tools: []oaiTool{{Type: "function", Function: &oaiToolFunction{Name: "read"}}}},
		{Role: "system", Tools: []oaiTool{}},
	}
	for i, message := range messages {
		want, err := message.MarshalJSON()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		got, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if bytes.Contains(want, []byte(`\ud83d`)) {
			// encoding/json rewrites a lone surrogate escape in a marshaler's output to U+FFFD; the request body is encoded with marshalRequestBody, which keeps it.
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("message %d:\n got %s\nwant %s", i, got, want)
		}
	}
	all, err := marshalRequestBody(struct {
		Messages []oaiMessage `json:"messages"`
	}{messages})
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	want.WriteString(`{"messages":[`)
	for i, message := range messages {
		if i > 0 {
			want.WriteByte(',')
		}
		encoded, _ := message.MarshalJSON()
		want.Write(encoded)
	}
	want.WriteString(`]}`)
	if wantBody := jsstring.UnescapeForStringify(want.Bytes()); !bytes.Equal(all, wantBody) {
		t.Errorf("request:\n got %s\nwant %s", all, wantBody)
	}
}
