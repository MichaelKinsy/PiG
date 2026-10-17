package ai

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

func validatedRecordCorpus() []string {
	corpus := []string{
		``, ` `, `{}`, `[]`, `null`, `0`, `-0`, `1.5`, `"s"`, `{"a":1}`, `{"a":1,"a":2}`, `{"a":"\u00e9\ud800"}`, `{"a":"\ud800"}`,
		"{\"a\":\"\xff\xfe\"}", "\xef\xbb\xbf{}", `{"a":1e400}`, `{"a":-1e400}`, `{"a":1e-400}`, `{"a":1E5}`, `[1.7976931348623157e308]`,
		`{"a":` + strings.Repeat("9", 300) + `}`, `{"a":` + strings.Repeat("9", 309) + `}`, `{"a":-` + strings.Repeat("9", 400) + `}`,
		`{"a":"` + strings.Repeat("9", 400) + `"}`, `{"a":"\"1e400"}`, `{"a":"\\"}`, `{"a":1} x`, `{"a":}`, `{"a":01}`, `{"a":1.}`, `{"a":.5}`,
		`{"a":NaN}`, `{"a":"x"`, `{"a":"\x"}`, `{"a":"\q\u12"}`, "{\"a\":\"line\nbreak\"}", "{\"a\":\"\x01\"}",
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001), strings.Repeat("[", 9999) + strings.Repeat("]", 9999),
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`,
	}
	random := rand.New(rand.NewPCG(3, 4))
	pieces := []string{`{`, `}`, `[`, `]`, `"a"`, `:`, `,`, `1`, `-`, `e`, `9`, `.`, `"\u0041"`, `"\\"`, `"\x"`, `true`, `null`, ` `, `"`, `400`, `E`, `+`}
	for range 20000 {
		var text strings.Builder
		for range 1 + random.IntN(12) {
			text.WriteString(pieces[random.IntN(len(pieces))])
		}
		corpus = append(corpus, text.String())
	}
	return corpus
}

// anthropicParseJSONByValidate is anthropicParseJSON before it reported encoding/json's acceptance: jsonparse.Validate of the text, then of its repair.
func anthropicParseJSONByValidate(data string) (string, error) {
	err := jsonparse.Validate([]byte(data))
	if err == nil {
		return data, nil
	}
	repaired := repairJSON(data)
	if repaired == data {
		return "", err
	}
	if err := jsonparse.Validate([]byte(repaired)); err != nil {
		return "", err
	}
	return repaired, nil
}

// anthropicParseJSON returns the text and error parseJsonWithRepair's port returned, and valid is json.Valid of that text: true only for text encoding/json accepts, which JSON.parse's acceptance does not imply (nesting past encoding/json's limit).
func TestAnthropicParseJSONReportsEncodingJSONValidity(t *testing.T) {
	invalidAccepted := 0
	for _, data := range validatedRecordCorpus() {
		text, valid, err := anthropicParseJSON(data)
		wantText, wantErr := anthropicParseJSONByValidate(data)
		if text != wantText || (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) {
			t.Fatalf("anthropicParseJSON(%.80q) = %.80q, %v; want %.80q, %v", data, text, err, wantText, wantErr)
		}
		if valid != (err == nil && json.Valid([]byte(text))) {
			t.Fatalf("anthropicParseJSON(%.80q) valid = %v, json.Valid of %.80q = %v", data, valid, text, json.Valid([]byte(text)))
		}
		if err == nil && !valid {
			invalidAccepted++
		}
	}
	if invalidAccepted == 0 {
		t.Fatal("no text that JSON.parse accepts and encoding/json rejects: the corpus does not exercise valid = false")
	}
}

// For text json.Valid accepts, the observer's skip check without the validation answers as the check with it, for both representations.
func TestValidJSONParsesAsValueAgreesOnValidText(t *testing.T) {
	checked, fast := 0, 0
	for _, text := range validatedRecordCorpus() {
		if !json.Valid([]byte(text)) {
			continue
		}
		checked++
		want := jsonParsesAsValue([]byte(text))
		if got := validJSONParsesAsValue(text); got != want {
			t.Fatalf("validJSONParsesAsValue(%.80q) = %v, jsonParsesAsValue = %v", text, got, want)
		}
		if got := validJSONParsesAsValue([]byte(text)); got != want {
			t.Fatalf("validJSONParsesAsValue([]byte(%.80q)) = %v, jsonParsesAsValue = %v", text, got, want)
		}
		if want {
			fast++
		}
	}
	if checked < 100 || fast == checked {
		t.Fatalf("checked %d valid texts, %d skippable: the corpus does not exercise both answers", checked, fast)
	}
}

// An unwanted observer on an Anthropic stream changes nothing a caller sees, as on Chat Completions: same result as an observer that acts and returns nil, including a record JSON.parse accepts that encoding/json cannot decode (nesting past its limit), and an observer that becomes wanted mid-stream receives every later event.
func TestUnwantedAnthropicProviderStreamEventObserverIsUnobservable(t *testing.T) {
	delta := func(text string) string {
		return `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`
	}
	start := `{"type":"message_start","message":{"id":"m","model":"claude","usage":{"input_tokens":1,"output_tokens":0}}}`
	blockStart := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	end := []string{`{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, `{"type":"message_stop"}`}
	deep := `{"type":"ping","x":` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`
	names := map[string]string{start: "message_start", blockStart: "content_block_start", end[0]: "content_block_stop", end[1]: "message_delta", end[2]: "message_stop", deep: "message_delta"}
	run := func(records []string, wanted func(int) bool) (*AssistantMessage, []any) {
		var body strings.Builder
		for _, record := range records {
			name := names[record]
			if name == "" {
				name = "content_block_delta"
			}
			body.WriteString("event: " + name + "\ndata: " + record + "\n\n")
		}
		var observed []any
		seen := 0
		builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "anthropic", "claude")
		options := StreamOptions{OnProviderStreamEvent: func(_ context.Context, data any, _ *Model) error { observed = append(observed, data); return nil }}
		if wanted != nil {
			options.ProviderStreamEventWanted = func() bool { seen++; return wanted(seen) }
		}
		builder.setProviderEventObserver(options, &Model{ID: "claude"})
		(&anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic", Model: "claude"}}).parseAnthropicSSE(t.Context(), strings.NewReader(body.String()), builder, anthropicStreamNames{})
		result := builder.stream.Result()
		result.Timestamp, result.DurationMs = 0, nil
		return result, observed
	}
	streams := map[string][]string{
		"text":           append([]string{start, blockStart, delta("a"), delta("b")}, end...),
		"overflow":       append([]string{start, blockStart, delta("a"), `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"b"},"x":1e400}`}, end...),
		"deep record":    append([]string{start, blockStart, delta("a"), deep, delta("b")}, end...),
		"invalid record": append([]string{start, blockStart, delta("a"), `{"type":"content_block_delta",`}, end...),
	}
	for name, records := range streams {
		always, observed := run(records, nil)
		never, none := run(records, func(int) bool { return false })
		if len(none) != 0 && name != "overflow" && name != "deep record" {
			t.Errorf("%s: an unwanted observer received %v", name, none)
		}
		if !reflect.DeepEqual(always, never) {
			t.Errorf("%s: unwanted observer result\n%#v\nwant\n%#v", name, never, always)
		}
		later, tail := run(records, func(seen int) bool { return seen > 3 })
		wantTail := observed[min(3, len(observed)):]
		if !reflect.DeepEqual(later, always) || len(tail) != len(wantTail) || len(tail) > 0 && !reflect.DeepEqual(tail, wantTail) {
			t.Errorf("%s: an observer wanted from the fourth event saw %v and ended %#v; want %v and %#v", name, tail, later, wantTail, always)
		}
		if name == "text" && len(wantTail) == 0 {
			t.Errorf("%s: the wanted observer saw no event; the stream does not exercise it", name)
		}
	}
}
