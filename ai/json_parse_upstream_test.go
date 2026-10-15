package ai

import (
	"reflect"
	"strings"
	"testing"
)

// Ports packages/ai/test/json-parse.test.ts (Pi 4b994d5e, #10868).
func TestParseStreamingJsonKeepsInvalidUnicodeEscapesAsLiteralText(t *testing.T) {
	cases := []struct {
		input string
		want  JsonObject
	}{
		{`{"text":"\url{x}"}`, JsonObject{"text": `\url{x}`}},
		{`{"path":"a.tex","content":"\usepackage{x}","mode":"w"}`, JsonObject{"path": "a.tex", "content": `\usepackage{x}`, "mode": "w"}},
	}
	for _, tc := range cases {
		if got := ParseStreamingJson(tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseStreamingJson(%s) = %#v, want %#v", tc.input, got, tc.want)
		}
	}
}

func TestParseStreamingJsonDecodesValidUnicodeEscapes(t *testing.T) {
	want := JsonObject{"text": `café \url`}
	if got := ParseStreamingJson(`{"text":"caf\u00e9 \url"}`); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseStreamingJsonOmitsTruncatedUnicodeEscape(t *testing.T) {
	want := JsonObject{"text": "ab"}
	if got := ParseStreamingJson(`{"text":"ab\u00`); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// Ports packages/ai/test/json-parse.test.ts parseToolCallArguments (Pi 87f9a364, #10897).
func TestParseToolCallArguments(t *testing.T) {
	if call := ParseToolCallArguments(`{"kind":"seek","note":"\url"}`); call.ArgumentsError != "" || !reflect.DeepEqual(call.Arguments, JsonObject{"kind": "seek", "note": `\url`}) {
		t.Errorf("repair: %#v %q", call.Arguments, call.ArgumentsError)
	}
	if args, _, msg := parseToolCallArguments(`{"kind":"seek","note":"\url"}`); msg != "" || !reflect.DeepEqual(args, JsonObject{"kind": "seek", "note": `\url`}) {
		t.Errorf("repair: %#v %q", args, msg)
	}
	if args, _, msg := parseToolCallArguments(""); msg != "" || len(args) != 0 {
		t.Errorf("empty: %#v %q", args, msg)
	}
	for _, raw := range []string{`{"action":"apply","actions":[{"kind":"seek","time":.5}]}`, `{"kind":"seek","clipId":"clip-fl`} {
		args, _, msg := parseToolCallArguments(raw)
		if len(args) != 0 || !strings.Contains(msg, "\n\nReceived arguments:\n") || !strings.HasSuffix(msg, raw) {
			t.Errorf("malformed %s: %#v %q", raw, args, msg)
		}
	}
	args, _, msg := parseToolCallArguments("[1,2]")
	if len(args) != 0 || msg != "Tool arguments must be a JSON object\n\nReceived arguments:\n[1,2]" {
		t.Errorf("array: %#v %q", args, msg)
	}
}
