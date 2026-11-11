package ai

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The SSE data lines are decoded in place (json.Unmarshal(jsonwire.Bytes(line), ...)) where json.Unmarshal([]byte(line)) copied each one first. These tests hold the
// results to what the copying decode gave, the error text included.

// referenceAnthropicDelta is decodeAnthropicDeltaEventReflect as it was written with the copy.
func referenceAnthropicDelta(sse anthropicStreamEvent, ev *anthEventContentBlockDelta) error {
	text := sse.JSON
	err := json.Unmarshal([]byte(text), ev)
	if _, ok := errorAsUnmarshalType(err); ok {
		if coerced, ok := coerceAnthropicDelta([]byte(text)); ok {
			*ev, err = coerced, nil
		}
	} else if err == nil {
		if member := anthropicDeltaMember(ev); member != nil && *member == "" {
			if coerced, ok := coerceAnthropicDelta([]byte(text)); ok {
				*ev = coerced
			}
		}
	}
	if err != nil {
		return &anthropicSSEParseError{sse: sse.serverSentEvent, cause: err}
	}
	return nil
}

func errorAsUnmarshalType(err error) (*json.UnmarshalTypeError, bool) {
	return errors.AsType[*json.UnmarshalTypeError](err)
}

func TestAnthropicDeltaReflectDecodesAsTheCopyingDecodeDid(t *testing.T) {
	for _, text := range []string{
		`{"index":0,"delta":{"type":"text_delta","text":"hi"}}`, `{"index":1e0,"delta":{"type":"text_delta","text":"x"}}`, `{"index":"1","delta":{"type":"text_delta","text":"x"}}`,
		`{"index":0,"delta":{"type":"text_delta","text":null}}`, `{"index":0,"delta":{"type":"input_json_delta"}}`, `{"index":0,"delta":{"type":"thinking_delta","thinking":5}}`,
		`{"delta":{"type":"text_delta","text":"\ud800 \u00e9 \" \\ "}}`, "{\"delta\":{\"type\":\"text_delta\",\"text\":\"a\xffb\xed\xa0\x80\"}}",
		`{"index":0,"d\u0065lta":{"type":"signature_delta","signature":"s"}}`, `{"INDEX":2,"DELTA":{"TEXT":"x"}}`, `[]`, `null`, `5`, `{"delta":[]}`, `{"delta":`,
		`{"index":-0,"delta":{"type":"text_delta","text":"x"}}`, `{"index":9007199254740993,"delta":{"type":"text_delta","text":"x"}}`,
	} {
		sse := anthropicStreamEvent{JSON: strings.Clone(text)}
		var got, want anthEventContentBlockDelta
		gotErr, wantErr := decodeAnthropicDeltaEventReflect(sse, &got), referenceAnthropicDelta(sse, &want)
		if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
			t.Errorf("%q: error %v, want %v", text, gotErr, wantErr)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n got: %+v\nwant: %+v", text, got, want)
		}
	}
}

func TestAnthropicEventTypeAndMemberLookupReadEscapedNamesAsBefore(t *testing.T) {
	for text, want := range map[string]string{
		`{"type":"message_start"}`: "message_start", `{"t\u0079pe":"message_stop"}`: "message_stop", `{"type":"message\u005fdelta"}`: "message_delta",
		`{"type":"a","type":"b\n"}`: "b\n", `{"TYPE":"x"}`: "", `{"type":5}`: "", `{"a\u0000":1,"type":"x"}`: "x",
	} {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			t.Fatal(err)
		}
		if got := anthropicEventType(strings.Clone(text)); got != want {
			t.Errorf("anthropicEventType(%q) = %q, want %q", text, got, want)
		}
	}
	for _, tc := range []struct {
		text, name string
		want       bool
	}{
		{`{"error":{"a":1}}`, "error", true}, {`{"err\u006fr":null}`, "error", true}, {`{"ERROR":1}`, "error", false}, {`{"a":1}`, "error", false},
		{`{"e\"rror":1,"error":2}`, `e"rror`, true}, {`{"x\ud800":1}`, "error", false},
	} {
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.text), &decoded); err != nil {
			t.Fatal(err)
		}
		_, set := decoded[tc.name]
		if got := jsonObjectHasMember(strings.Clone(tc.text), tc.name); got != tc.want || set != tc.want {
			t.Errorf("jsonObjectHasMember(%q, %q) = %v, map holds it = %v, want %v", tc.text, tc.name, got, set, tc.want)
		}
	}
}

// BenchmarkAnthropicDeltaReflect is the decode of a content_block_delta that the fast decode leaves (a member name spelled with an escape).
func BenchmarkAnthropicDeltaReflect(b *testing.B) {
	sse := anthropicStreamEvent{JSON: `{"index":0,"d\u0065lta":{"type":"text_delta","text":"` + strings.Repeat("streamed text ", 8) + `"}}`}
	b.SetBytes(int64(len(sse.JSON)))
	b.ReportAllocs()
	for b.Loop() {
		var ev anthEventContentBlockDelta
		if err := decodeAnthropicDeltaEventReflect(sse, &ev); err != nil {
			b.Fatal(err)
		}
	}
}
