//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"testing"
)

// The typed lowering writes the bytes the general lowering of the request's JSON does, for the member shapes the converter makes and the edge ones
// the struct tags it replaces handled: empty and nil values, escapes a JavaScript string keeps, and a lone UTF-16 unit.
func TestMistralWireMessagesMatchTheGeneralLowering(t *testing.T) {
	text, empty, prefix, noPrefix := "t<&>\u2028é", "", true, false
	surrogate := "lone \xed\xa0\x80 unit"
	cases := map[string][]mistralMessage{
		"string content":            {{Role: "user", Content: "hi <b>&amp;</b>\u2029"}},
		"empty string content kept": {{Role: "system", Content: ""}},
		"no content":                {{Role: "assistant", Prefix: &prefix}},
		"prefix false":              {{Role: "assistant", Prefix: &noPrefix, Content: []mistralContentChunk{}}},
		"empty text kept":           {{Role: "user", Content: []mistralContentChunk{{Type: "text", Text: &empty}}}},
		"text and image":            {{Role: "user", Content: []mistralContentChunk{{Type: "text", Text: &text}, {Type: "image_url", ImageURL: "data:image/png;base64,AA=="}}}},
		"thinking":                  {{Role: "assistant", Prefix: &noPrefix, Content: []mistralContentChunk{{Type: "thinking", Thinking: []mistralTextChunk{{Type: "text", Text: text}, {Type: "text", Text: ""}}}}}},
		"thinking empty":            {{Role: "assistant", Content: []mistralContentChunk{{Type: "thinking", Thinking: []mistralTextChunk{}}}}},
		"thinking nil":              {{Role: "assistant", Content: []mistralContentChunk{{Type: "thinking", Thinking: []mistralTextChunk(nil)}}}},
		"thinking other":            {{Role: "assistant", Content: []mistralContentChunk{{Type: "thinking", Thinking: "plain"}}}},
		"tool calls":                {{Role: "assistant", Prefix: &noPrefix, ToolCalls: []mistralToolCallMsg{{ID: "abc123456", Type: "function", Function: mistralToolCallFnMsg{Name: "n", Arguments: `{"a":"<&>"}`}, Index: 2}}}},
		"tool calls empty":          {{Role: "assistant", ToolCalls: []mistralToolCallMsg{}}},
		"tool result":               {{Role: "tool", ToolCallID: "abc123456", Name: "n", Content: []mistralContentChunk{{Type: "text", Text: &text}}}},
		"name only":                 {{Role: "user", Name: "ann", Content: "x"}},
		"lone surrogate":            {{Role: "user", Content: surrogate}, {Role: "assistant", Content: []mistralContentChunk{{Type: "text", Text: &surrogate}}}},
	}
	for name, messages := range cases {
		t.Run(name, func(t *testing.T) {
			request := mistralRequest{Model: "m", Stream: true, Messages: messages}
			typed, err := toMistralWirePayload(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := marshalRequestBody(request)
			if err != nil {
				t.Fatal(err)
			}
			general, err := toMistralWirePayload(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if string(typed) != string(general) {
				t.Fatalf("typed lowering\n got: %s\nwant: %s", typed, general)
			}
		})
	}
}

// A list without messages and a chunk list that is nil are written as the empty arrays the lowering has always written; the general lowering of
// their JSON differs (it rejects a null messages as Pi's payload.messages.map does, and keeps a null content), and the typed one never meets either.
func TestMistralWireMessagesWriteEmptyArraysForNil(t *testing.T) {
	for name, test := range map[string]struct {
		messages []mistralMessage
		want     string
	}{
		"no messages": {nil, `{"model":"m","stream":true,"messages":[]}`},
		"nil chunks":  {[]mistralMessage{{Role: "user", Content: []mistralContentChunk(nil)}}, `{"model":"m","stream":true,"messages":[{"role":"user","content":[]}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := toMistralWirePayload(mistralRequest{Model: "m", Stream: true, Messages: test.messages})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("got %s want %s", got, test.want)
			}
		})
	}
}

// The encoder that keeps a lone UTF-16 unit as an escape writes the same members, in the same order, as the one that writes the messages itself.
func TestMistralWireMessagesMarshalJSONMatchesMarshalJSONTo(t *testing.T) {
	text, prefix := "t<&>\u2028é", false
	messages := mistralWireMessages{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Prefix: &prefix, Content: []mistralContentChunk{{Type: "text", Text: &text}, {Type: "thinking", Thinking: []mistralTextChunk{{Type: "text", Text: "hm"}}}, {Type: "image_url", ImageURL: "data:x"}},
			ToolCalls: []mistralToolCallMsg{{ID: "abc123456", Type: "function", Function: mistralToolCallFnMsg{Name: "n", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "abc123456", Name: "n", Content: []mistralContentChunk{{Type: "text", Text: &text}}},
	}
	viaTo, err := marshalRequestBody(struct {
		Messages mistralWireMessages `json:"messages"`
	}{messages})
	if err != nil {
		t.Fatal(err)
	}
	viaMarshalJSON, err := messages.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"messages":` + string(viaMarshalJSON) + `}`; string(viaTo) != want {
		t.Fatalf("MarshalJSONTo\n got: %s\nwant: %s", viaTo, want)
	}
}
