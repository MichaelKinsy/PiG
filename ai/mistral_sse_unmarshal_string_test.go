//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Mistral half of sse_unmarshal_string_test.go: it calls parseMistralEvent, which a stripped build does not have.

// referenceMistralEvent is parseMistralEvent's decode of one data text, as it was written with the copy.
func referenceMistralEvent(text string) (json.RawMessage, bool) {
	var record map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &record) != nil || !strings.HasPrefix(string(record["choices"]), "[") {
		return nil, false
	}
	return json.RawMessage(text), true
}

func TestParseMistralEventDecodesAsTheCopyingDecodeDid(t *testing.T) {
	for _, text := range []string{
		`{"choices":[]}`, `{"choices":[{"delta":{"content":"hi"}}],"usage":{"prompt_tokens":1}}`, `{ "choices" : [ ] }`,
		`{"choices":[],"choices":"x"}`, `{"choices":"x","choices":[]}`, `{"ch\u006fices":[]}`, `{"CHOICES":[]}`, `{"choices":null}`, `{"choices":{"a":[]}}`,
		`{"choices":"[x"}`, `[]`, `null`, `5`, `"s"`, `{"a":1}`, `{"choices":[1e400]}`, `{"choices":["\ud800"]}`, "{\"choices\":[\"\xed\xa0\x80\"]}",
		`{"choices":[]} x`, `{"choices":[]`, `{"choices":[],}`, `{"choices":[-0]}`, strings.Repeat(`[`, 600) + strings.Repeat(`]`, 600),
		`{"choices":` + strings.Repeat(`[`, 600) + strings.Repeat(`]`, 600) + `}`,
	} {
		raw := "data: " + strings.Clone(text) + "\n"
		event, done, err := parseMistralEvent(raw)
		want, ok := referenceMistralEvent(text)
		switch {
		case done:
			t.Errorf("%q: done", text)
		case !json.Valid([]byte(text)):
			if err == nil {
				t.Errorf("%q: accepted invalid JSON as %q", text, event)
			}
		case ok != (err == nil):
			t.Errorf("%q: event %q, error %v; the copying decode accepted=%v", text, event, err, ok)
		case ok && string(event) != string(want):
			t.Errorf("%q: event %q, want %q", text, event, want)
		}
	}
}

// BenchmarkMistralParseEvent is the decode of each data line of a Mistral stream.
func BenchmarkMistralParseEvent(b *testing.B) {
	raw := "data: " + `{"id":"cmpl-1","object":"chat.completion.chunk","created":1,"model":"mistral-large-latest","choices":[{"index":0,"delta":{"content":"` + strings.Repeat("streamed text ", 8) + `"},"finish_reason":null}]}` + "\n"
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		if event, _, err := parseMistralEvent(raw); err != nil || event == nil {
			b.Fatal(err)
		}
	}
}
