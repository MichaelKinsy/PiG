package ai

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

type viaMarshalJSON struct{ value json.Marshaler }

func (v viaMarshalJSON) MarshalJSON() ([]byte, error) { return v.value.MarshalJSON() }

// encoding/json writes the same bytes through MarshalJSONTo as through MarshalJSON, with its own options and with the JSON.stringify ones.
func TestContentMarshalJSONToWritesWhatMarshalJSONWrites(t *testing.T) {
	lone := "lone \xed\xa0\x80 unit"
	call := ToolCall{ID: "c<1>", Name: "read", Arguments: JsonObject{"path": "a<b>&c\u2028", "n": 1.5}, ThoughtSignature: "s", Namespace: "n"}
	for name, value := range map[string]json.Marshaler{
		"text":           TextContent{Text: "a <b> & c \u2029 é", TextSignature: "sig"},
		"text lone":      TextContent{Text: lone},
		"text empty":     TextContent{},
		"image":          ImageContent{Data: "QUJD", MimeType: "image/png"},
		"thinking":       ThinkingContent{Thinking: "hm <&>", ThinkingSignature: "x", Redacted: true},
		"thinking empty": ThinkingContent{thinkingSignatureEmpty: true},
		"tool call":      call,
		"tool call nil":  ToolCall{ID: "i", Name: "n"},
		"usage":          Usage{Input: 1, Output: 2, CacheRead: 3, TotalTokens: 6, Cost: UsageCost{Input: 0.1, Total: 0.25}},
		"usage splits":   Usage{Input: 1, splitsAfterCost: true},
		"blocks":         ContentBlocks{TextContent{Text: "<&>"}, ImageContent{Data: "d", MimeType: "m"}, call, ThinkingContent{Thinking: lone}},
		"blocks empty":   ContentBlocks{},
		"blocks nil":     ContentBlocks(nil),
	} {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(viaMarshalJSON{value})
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("json.Marshal\n got: %s\nwant: %s", got, want)
			}
			want, err = jsonv2.Marshal(viaMarshalJSON{value}, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			got, err = jsonv2.Marshal(value, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("jsonv2.Marshal(Stringify)\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
