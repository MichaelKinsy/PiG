package ai

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"math"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

type viaMarshalJSON struct{ value json.Marshaler }

func (v viaMarshalJSON) MarshalJSON() ([]byte, error) { return v.value.MarshalJSON() }

// encoding/json writes the same bytes through MarshalJSONTo as through MarshalJSON, with its own options and with the JSON.stringify ones.
func TestContentMarshalJSONToWritesWhatMarshalJSONWrites(t *testing.T) {
	lone := "lone \xed\xa0\x80 unit"
	call := ToolCall{ID: "c<1>", Name: "read", Arguments: JsonObject{"path": "a<b>&c\u2028", "n": 1.5}, ThoughtSignature: "s", Namespace: "n"}
	negZero := math.Copysign(0, -1)
	zeroCall := ToolCall{ID: "c\xff", Name: "read", Arguments: JsonObject{"neg": negZero, "nested": []any{negZero, map[string]any{"z": negZero}}, "s": "\xc0\xaf"}}
	for name, value := range map[string]json.Marshaler{
		// Negative zero and invalid UTF-8 are written as JSON.stringify writes them: 0, and the replacement character itself.
		"text invalid":      TextContent{Text: "inv \xff", TextSignature: "\xfe"},
		"thinking invalid":  ThinkingContent{Thinking: "inv \xff", ThinkingSignature: "\xc0"},
		"tool call zero":    zeroCall,
		"usage zero":        Usage{Input: 1, TotalTokens: 1, Cost: UsageCost{Input: negZero, Output: -0.5, CacheRead: negZero, CacheWrite: negZero, Total: negZero}},
		"usage zero splits": Usage{Input: 1, splitsAfterCost: true, Cost: UsageCost{Input: negZero}},
		"blocks zero":       ContentBlocks{TextContent{Text: "\xff"}, zeroCall, ThinkingContent{Thinking: "\xff"}},
		"text":              TextContent{Text: "a <b> & c \u2029 é", TextSignature: "sig"},
		"text lone":         TextContent{Text: lone},
		"text empty":        TextContent{},
		"image":             ImageContent{Data: "QUJD", MimeType: "image/png"},
		"thinking":          ThinkingContent{Thinking: "hm <&>", ThinkingSignature: "x", Redacted: true},
		"thinking empty":    ThinkingContent{thinkingSignatureEmpty: true},
		"tool call":         call,
		"tool call nil":     ToolCall{ID: "i", Name: "n"},
		"usage":             Usage{Input: 1, Output: 2, CacheRead: 3, TotalTokens: 6, Cost: UsageCost{Input: 0.1, Total: 0.25}},
		"usage splits":      Usage{Input: 1, splitsAfterCost: true},
		"blocks":            ContentBlocks{TextContent{Text: "<&>"}, ImageContent{Data: "d", MimeType: "m"}, call, ThinkingContent{Thinking: lone}},
		"blocks empty":      ContentBlocks{},
		"blocks nil":        ContentBlocks(nil),
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
			if !bytes.Equal(got, want) {
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
			if !bytes.Equal(got, want) {
				t.Fatalf("jsonv2.Marshal(Stringify)\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// The bytes are the ones Pi's JSON.stringify writes: negative zero is 0 and a byte that is not UTF-8 is the replacement character, raw, from
// every encoder that reaches the value.
func TestContentMarshalJSONWritesWhatJSONStringifyWrites(t *testing.T) {
	negZero := math.Copysign(0, -1)
	zeroCall := ToolCall{ID: "c", Name: "read", Arguments: JsonObject{"neg": negZero, "s": "\xff"}}
	for name, value := range map[string]json.Marshaler{
		"text":       TextContent{Text: "inv \xff"},
		"thinking":   ThinkingContent{Thinking: "inv \xff"},
		"tool call":  zeroCall,
		"usage":      Usage{Cost: UsageCost{Input: negZero, Total: negZero}},
		"usage tail": Usage{splitsAfterCost: true, Cost: UsageCost{Input: negZero}},
		"blocks":     ContentBlocks{TextContent{Text: "\xff"}, zeroCall},
	} {
		t.Run(name, func(t *testing.T) {
			direct, err := value.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			viaJSON, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			viaStringify, err := jsonv2.Marshal(value, jsonopts.Stringify)
			if err != nil {
				t.Fatal(err)
			}
			for form, text := range map[string]string{"MarshalJSON": string(direct), "json.Marshal": string(viaJSON), "Stringify": string(viaStringify)} {
				if negativeZero.MatchString(text) {
					t.Errorf("%s writes a negative zero: %s", form, text)
				}
				if strings.Contains(text, `\ufffd`) || !utf8.ValidString(text) {
					t.Errorf("%s writes the replacement character as an escape or keeps an ill-formed byte: %s", form, text)
				}
			}
		})
	}
}

// negativeZero matches a JSON number that is a negative zero without a fraction or an exponent.
var negativeZero = regexp.MustCompile(`(^|[^0-9eE.])-0([^.0-9eE]|$)`)

// A provider request body carries a content block with the bytes JSON.stringify writes for it: negative zero is 0 and an ill-formed byte is the
// replacement character, raw.
func TestRequestBodyWritesContentBlocksAsJSONStringifyDoes(t *testing.T) {
	negZero := math.Copysign(0, -1)
	body := struct {
		Content ContentBlocks `json:"content"`
		Usage   Usage         `json:"usage"`
	}{
		Content: ContentBlocks{TextContent{Text: "inv \xff"}, ToolCall{ID: "c", Name: "n", Arguments: JsonObject{"neg": negZero}}},
		Usage:   Usage{Cost: UsageCost{Input: negZero}},
	}
	got, err := marshalRequestBody(body)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"content":[{"type":"text","text":"inv \ufffd"},{"type":"toolCall","id":"c","name":"n","arguments":{"neg":0}}],` +
		`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`
	if string(got) != strings.ReplaceAll(want, `\ufffd`, "\ufffd") {
		t.Fatalf("got:  %s\nwant: %s", got, strings.ReplaceAll(want, `\ufffd`, "\ufffd"))
	}
}
