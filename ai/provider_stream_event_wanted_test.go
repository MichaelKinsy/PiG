package ai

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// jsonParsesAsValue may answer false for text that parses, but never true for text json.Unmarshal into an any rejects: an unwanted observer skips the parse only when the parse cannot fail.
func TestJSONParsesAsValueNeverClaimsAFailingParse(t *testing.T) {
	corpus := []string{
		``, ` `, `{}`, `[]`, `null`, `0`, `-0`, `1.5`, `"s"`, `{"a":1}`, `{"a":1,"a":2}`, `{"a":"\u00e9\ud800"}`, `{"a":"\ud800"}`,
		"{\"a\":\"\xff\xfe\"}", "\xef\xbb\xbf{}", `{"a":1e400}`, `{"a":-1e400}`, `{"a":1e-400}`, `{"a":1E5}`, `[1.7976931348623157e308]`,
		`{"a":` + strings.Repeat("9", 300) + `}`, `{"a":` + strings.Repeat("9", 309) + `}`, `{"a":-` + strings.Repeat("9", 400) + `}`,
		`{"a":"` + strings.Repeat("9", 400) + `"}`, `{"a":"\"1e400"}`, `{"a":"\\"}`, `{"a":1} x`, `{"a":}`, `{"a":01}`, `{"a":1.}`, `{"a":.5}`,
		`{"a":NaN}`, `{"a":Infinity}`, `{"a":tru}`, "{\"a\":\"\x01\"}", `{"a":"x"`, strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
		strings.Repeat("[", 9999) + strings.Repeat("]", 9999), `{"type":"response.output_text.delta","delta":"t1 ","sequence_number":3}`,
	}
	random := rand.New(rand.NewPCG(1, 2))
	pieces := []string{`{`, `}`, `[`, `]`, `"a"`, `:`, `,`, `1`, `-`, `e`, `9`, `.`, `"\u0041"`, `"\\"`, `true`, `null`, ` `, `"`, `400`, `E`, `+`}
	for range 20000 {
		var text strings.Builder
		for range 1 + random.IntN(12) {
			text.WriteString(pieces[random.IntN(len(pieces))])
		}
		corpus = append(corpus, text.String())
	}
	fast := 0
	for _, text := range corpus {
		var value any
		parses := json.Unmarshal([]byte(text), &value) == nil
		if jsonParsesAsValue([]byte(text)) {
			fast++
			if !parses {
				t.Errorf("jsonParsesAsValue(%.80q) = true, but json.Unmarshal fails", text)
			}
		}
	}
	if fast < 100 {
		t.Errorf("only %d texts took the fast answer; the corpus does not exercise it", fast)
	}
}

// An unwanted observer changes nothing a caller sees: the same events and errors as an observer that acts and returns nil, a wire event that fails to parse still ends the request with the same error, and an observer that becomes wanted mid-stream receives every later event as parsed.
func TestUnwantedProviderStreamEventObserverIsUnobservable(t *testing.T) {
	frames := []string{
		`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"c"},"finish_reason":null}],"x":1e400}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	run := func(frames []string, wanted func(int) bool) (*AssistantMessage, []any) {
		var body strings.Builder
		for _, frame := range frames {
			body.WriteString("data: " + frame + "\n\n")
		}
		body.WriteString("data: [DONE]\n\n")
		var observed []any
		seen := 0
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openai", "model")
		options := StreamOptions{OnProviderStreamEvent: func(_ context.Context, data any, _ *Model) error { observed = append(observed, data); return nil }}
		if wanted != nil {
			options.ProviderStreamEventWanted = func() bool { seen++; return wanted(seen) }
		}
		builder.setProviderEventObserver(options, &Model{ID: "model"})
		(&openAIProvider{}).parseSSE(t.Context(), strings.NewReader(body.String()), builder, nil)
		result := builder.stream.Result()
		result.Timestamp, result.DurationMs = 0, nil
		return result, observed
	}
	for name, frames := range map[string][]string{"parsing": {frames[0], frames[1], frames[3]}, "overflowing number": frames, "invalid JSON": {frames[0], `{"choices":[`, frames[3]}} {
		always, observed := run(frames, nil)
		never, none := run(frames, func(int) bool { return false })
		if len(none) != 0 {
			t.Errorf("%s: an unwanted observer received %v", name, none)
		}
		if !reflect.DeepEqual(always, never) {
			t.Errorf("%s: unwanted observer result\n%+v\nwant the result of an acting observer\n%+v", name, *never, *always)
		}
		later, tail := run(frames, func(seen int) bool { return seen > 1 })
		if want := observed[min(1, len(observed)):]; !reflect.DeepEqual(always, later) || len(tail) != len(want) || len(want) > 0 && !reflect.DeepEqual(want, tail) {
			t.Errorf("%s: an observer wanted from the second event received %v, want %v", name, tail, observed[min(1, len(observed)):])
		}
	}
	if result, _ := run(frames, func(int) bool { return false }); result.StopReason != StopReasonError || result.ErrorMessage == "" {
		t.Errorf("an overflowing number with an unwanted observer = %q %q, want the parse error", result.StopReason, result.ErrorMessage)
	}
}
