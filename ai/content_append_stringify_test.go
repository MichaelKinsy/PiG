package ai

import (
	"math"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// appendStringifyTexts are strings whose JSON.stringify form differs from encoding/json's: HTML characters, the line and paragraph
// separators, controls, lone and paired surrogate halves held as WTF-8, and ill-formed UTF-8.
var appendStringifyTexts = []string{
	"", "plain", `<a href="x">&amp;</a>`, "line\u2028para\u2029", "\x00\x01\x1f\x7f\b\f\n\r\t\"\\/", "\xed\xa0\x80 lone high",
	"\xed\xb0\x80 lone low", "\xed\xa0\x80\xed\xb0\x80 pair", "\xff\xfe\xc3 \xe2\x82 ill-formed", "\ufffd é 😀",
}

// assertAppendStringifyMatches checks that value's AppendStringify appends what the JSON.stringify mode writes for its MarshalJSON
// output, and that the codec splices it in place.
func assertAppendStringifyMatches(t *testing.T, value interface {
	sdkjson.StringifyAppender
}) {
	t.Helper()
	marshaled, err := value.MarshalJSON()
	if err != nil {
		t.Fatalf("%T MarshalJSON: %v", value, err)
	}
	want, err := sdkjson.MarshalStringify(sdkjson.RawMessage(marshaled))
	if err != nil {
		t.Fatal(err)
	}
	appended, err := value.AppendStringify([]byte("prefix:"))
	if err != nil {
		t.Fatalf("%T AppendStringify: %v", value, err)
	}
	if string(appended) != "prefix:"+string(want) {
		t.Fatalf("%T AppendStringify wrote\n%s\nwant prefix:%s", value, appended, want)
	}
	spliced, err := sdkjson.MarshalStringify([]any{value})
	if err != nil {
		t.Fatal(err)
	}
	if string(spliced) != "["+string(want)+"]" {
		t.Fatalf("%T in an array writes\n%s\nwant [%s]", value, spliced, want)
	}
}

// Each content block's AppendStringify writes what its MarshalJSON writes in the JSON.stringify mode, for every kind of string.
func TestContentAppendStringifyMatchesMarshalJSON(t *testing.T) {
	for _, text := range appendStringifyTexts {
		assertAppendStringifyMatches(t, TextContent{Text: text})
		assertAppendStringifyMatches(t, TextContent{Text: text, TextSignature: text + "sig"})
		assertAppendStringifyMatches(t, ThinkingContent{Thinking: text, ThinkingSignature: text, Redacted: true})
		assertAppendStringifyMatches(t, ImageContent{Data: text, MimeType: "image/png"})
		assertAppendStringifyMatches(t, ToolCall{ID: text, Name: "write", ThoughtSignature: text, Namespace: "ns",
			Arguments: JsonObject{"path": text, "z": math.Copysign(0, -1), "n": []any{1.5, 1e21, 1e-7, text, nil, true}, text: map[string]any{"<": "&"}}})
		var ordered ToolCall
		if err := ordered.SetArgumentsJSON([]byte(`{"z":1,"a":{"y":-0,"b":"<&>"},"10":[2],"1":"x"}`)); err != nil {
			t.Fatal(err)
		}
		ordered.ID, ordered.Arguments["text"] = text, text
		assertAppendStringifyMatches(t, ordered)
	}
	var decoded ThinkingContent
	if err := sdkjson.Unmarshal([]byte(`{"type":"thinking","thinking":"t","thinkingSignature":""}`), &decoded); err != nil {
		t.Fatal(err)
	}
	assertAppendStringifyMatches(t, decoded)
	for _, record := range []string{`{"type":"toolCall","id":"c","name":"n","arguments":[1,-0,"<"]}`, `{"type":"toolCall","id":"c","name":"n","arguments":null}`} {
		var call ToolCall
		if err := call.UnmarshalJSON([]byte(record)); err != nil {
			t.Fatal(err)
		}
		assertAppendStringifyMatches(t, call)
	}
	assertAppendStringifyMatches(t, ToolCall{ID: "nil arguments"})
}

// A tool call whose arguments have no JSON form reports MarshalJSON's error from AppendStringify and appends nothing.
func TestToolCallAppendStringifyReportsMarshalJSONError(t *testing.T) {
	call := ToolCall{ID: "c", Name: "n", Arguments: JsonObject{"f": func() {}}}
	_, want := call.MarshalJSON()
	if want == nil {
		t.Fatal("MarshalJSON accepted a function argument")
	}
	got, err := call.AppendStringify([]byte("prefix"))
	if err == nil || err.Error() != want.Error() || string(got) != "prefix" {
		t.Fatalf("AppendStringify = %q, %v; want prefix, %v", got, err, want)
	}
}
