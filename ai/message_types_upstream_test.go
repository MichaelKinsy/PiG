package ai

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// upstream: packages/ai/test/message-types.test.ts describe "message JSON types"

func toolResultWithDetails(details any) ToolResultMessage {
	return ToolResultMessage{ToolCallID: "call-1", ToolName: "read", Content: []ToolResultMessageContent{}, Details: details, Timestamp: 1}
}

func TestToolResultMessagePreservesJSONCompatibleDetailTypes(t *testing.T) {
	// upstream: message-types.test.ts:14 "preserves JSON-compatible detail types without requiring index signatures"
	type summary struct {
		Lines int `json:"lines,omitempty"`
	}
	type details struct {
		Path    string   `json:"path"`
		Items   []string `json:"items"`
		Summary *summary `json:"summary,omitempty"`
	}
	cases := []struct {
		name    string
		details any
		want    string
	}{
		{"object", details{Path: "a.ts", Items: []string{"first"}, Summary: &summary{Lines: 3}}, `{"path":"a.ts","items":["first"],"summary":{"lines":3}}`},
		{"array", []string{"a", "b"}, `["a","b"]`},
		{"tuple", []any{"a", map[string]int{"count": 1}}, `["a",{"count":1}]`},
		{"primitive", "diagnostic", `"diagnostic"`},
		{"null", nil, `null`},
	}
	messages := make([]Message, 0, len(cases))
	for _, tc := range cases {
		messages = append(messages, toolResultWithDetails(tc.details))
	}
	transcript := NormalizeContext(Context{Messages: messages})
	if err := validateTranscriptContext(transcript); err != nil {
		t.Fatalf("JSON-compatible details were rejected: %v", err)
	}
	got := transcript.Messages()
	if len(got) != len(cases) {
		t.Fatalf("transcript holds %d messages, want %d", len(got), len(cases))
	}
	for index, tc := range cases {
		message, ok := got[index].(ToolResultMessage)
		if !ok {
			t.Fatalf("%s: message %d is %T", tc.name, index, got[index])
		}
		encoded, err := json.Marshal(message.Details)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		// Normalization rebuilds a struct as an object, so compare the decoded values (toEqual ignores key order).
		var got, want any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: details = %s, want %s", tc.name, encoded, tc.want)
		}
	}
}

func TestToolResultMessageRejectsNonJSONDetailTypes(t *testing.T) {
	// upstream: message-types.test.ts:49 "rejects non-JSON detail types"
	// Upstream rejects these at compile time through expectTypeOf on a generic parameter; Go's Details is any, so NormalizeContext rejects the values that JSON cannot carry before a provider sees them.
	cases := []struct {
		name    string
		details any
	}{
		{"function", map[string]any{"callback": func() {}}},
		{"channel", map[string]any{"value": make(chan int)}},
		{"not a number", map[string]any{"value": math.NaN()}},
		{"infinity", []any{math.Inf(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transcript := NormalizeContext(Context{Messages: []Message{toolResultWithDetails(tc.details)}})
			if err := validateTranscriptContext(transcript); err == nil {
				t.Fatalf("details %#v were accepted", tc.details)
			}
			if messages := transcript.Messages(); messages != nil {
				t.Fatalf("rejected transcript still exposes messages: %#v", messages)
			}
		})
	}
}
