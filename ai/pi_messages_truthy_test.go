package ai

import (
	"encoding/json"
	"testing"
)

// legacyParsePiMessagesEvent is the parse before jsonTruthy: the value decoded into any and switched on.
func legacyParsePiMessagesEvent(data string) (json.RawMessage, bool, error) {
	var value any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return nil, false, err
	}
	switch value := value.(type) {
	case nil:
		return nil, false, nil
	case bool:
		return json.RawMessage(data), value, nil
	case float64:
		return json.RawMessage(data), value != 0, nil
	case string:
		return json.RawMessage(data), value != "", nil
	}
	return json.RawMessage(data), true, nil
}

// An event line whose JSON value is falsy is no event; the answer, including the error for a top-level number that float64 cannot hold, is the one the decode into any gave. A nested out-of-range number is the exception (TestParsePiMessagesEventAcceptsNestedOutOfRangeNumberAsJSONParseDoes).
func TestParsePiMessagesEventTruthinessMatchesTheDecodeIntoAny(t *testing.T) {
	values := []string{`null`, `true`, `false`, `0`, `-0`, `0.0`, `0e5`, `-0e-3`, `1`, `-1`, `0.5`, `1e400`, `-1e400`, `1e-400`, `123456789012345678901234567890`,
		`""`, `"a"`, `"0"`, `"false"`, `"\u0000"`, `"\ud83d"`, `{}`, `{"type":"start"}`, `[]`, `[0]`, `[[],{}]`, "\"\xff\"", `"é"`}
	for _, value := range values {
		line := "data: " + value
		gotRaw, gotOK, gotErr := parsePiMessagesEvent(line)
		wantRaw, wantOK, wantErr := legacyParsePiMessagesEvent(value)
		if (gotErr != nil) != (wantErr != nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
			t.Errorf("%s: error %v, want %v", value, gotErr, wantErr)
			continue
		}
		if gotOK != wantOK || string(gotRaw) != string(wantRaw) {
			t.Errorf("%s: (%q, %v), want (%q, %v)", value, gotRaw, gotOK, wantRaw, wantOK)
		}
	}
}

// pi-messages.ts:313-320 parsePiMessagesEvent is JSON.parse(data), which reads an out-of-range number inside an object or array as Infinity, so the event line is an event. jsonTruthy reads only the top-level token and accepts it too; the decode into any that it replaced rejected it, so this is the one answer the replacement changes.
func TestParsePiMessagesEventAcceptsNestedOutOfRangeNumberAsJSONParseDoes(t *testing.T) {
	for _, value := range []string{`{"type":"start","x":1e400}`, `{"type":"start","extra":{"n":-1e999}}`, `[1e400]`} {
		raw, ok, err := parsePiMessagesEvent("data: " + value)
		if err != nil || !ok || string(raw) != value {
			t.Errorf("%s: (%q, %v, %v), want the event", value, raw, ok, err)
		}
	}
	converter := newPiMessagesEventConverter("test", "model")
	if _, err := converter.convert(json.RawMessage(`{"type":"start","x":1e400}`)); err != nil {
		t.Errorf("start event with an unknown out-of-range member: %v", err)
	}
}

// A text end event forwards its contentSignature and a thinking end event forwards its contentSignature and redacted flag only when the JSON has them, and an absent member stays absent.
func TestPiMessagesEndEventsForwardOnlyPresentMembers(t *testing.T) {
	converter := newPiMessagesEventConverter("test", "model")
	feed := func(events ...string) []AssistantMessageEvent {
		var out []AssistantMessageEvent
		for _, raw := range events {
			converted, err := converter.convert(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			out = append(out, converted...)
		}
		return out
	}
	events := feed(
		`{"type":"start"}`,
		`{"type":"text_start","contentIndex":0}`, `{"type":"text_end","contentIndex":0,"content":"a"}`,
		`{"type":"text_start","contentIndex":1}`, `{"type":"text_end","contentIndex":1,"content":"b","contentSignature":""}`,
		`{"type":"thinking_start","contentIndex":2}`, `{"type":"thinking_end","contentIndex":2,"content":"c","redacted":false}`,
		`{"type":"thinking_start","contentIndex":3}`, `{"type":"thinking_end","contentIndex":3,"content":"d","contentSignature":"s","redacted":true}`,
	)
	var ends []AssistantMessageEvent
	for _, event := range events {
		switch event.(type) {
		case TextEndEvent, ThinkingEndEvent:
			ends = append(ends, event)
		}
	}
	if len(ends) != 4 {
		t.Fatalf("%d end events", len(ends))
	}
	if end := ends[0].(TextEndEvent); end.contentSignature != nil {
		t.Errorf("absent text signature forwarded as %q", *end.contentSignature)
	}
	if end := ends[1].(TextEndEvent); end.contentSignature == nil || *end.contentSignature != "" {
		t.Errorf("empty text signature = %v", end.contentSignature)
	}
	if end := ends[2].(ThinkingEndEvent); end.contentSignature != nil || end.redacted == nil || *end.redacted {
		t.Errorf("thinking end = %v %v", end.contentSignature, end.redacted)
	}
	if end := ends[3].(ThinkingEndEvent); end.contentSignature == nil || *end.contentSignature != "s" || end.redacted == nil || !*end.redacted {
		t.Errorf("thinking end = %v %v", end.contentSignature, end.redacted)
	}
	for _, bad := range []string{`{"type":"text_end","contentIndex":1,"content":"b","contentSignature":5}`, `{"type":"thinking_end","contentIndex":2,"content":"c","redacted":"no"}`} {
		if _, err := converter.convert(json.RawMessage(bad)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// JSON.parse accepts a number too large for a double anywhere in an event and makes it Infinity (pi-messages.ts:320), so an event whose ignored member holds one still streams. The decode into any rejected it; only the top-level value is decoded now, as a top-level 1e400 still is.
func TestParsePiMessagesEventAcceptsANestedNumberJSONParseAccepts(t *testing.T) {
	converter := newPiMessagesEventConverter("test", "model")
	for _, event := range []string{`{"type":"start","extra":1e400}`, `{"type":"text_start","contentIndex":0,"extra":[-1e999]}`, `{"type":"text_delta","contentIndex":0,"delta":"x","extra":{"n":1e400}}`} {
		raw, ok, err := parsePiMessagesEvent("data: " + event)
		if err != nil || !ok || string(raw) != event {
			t.Fatalf("%s: (%q, %v, %v), want the event", event, raw, ok, err)
		}
		if _, err := converter.convert(raw); err != nil {
			t.Fatalf("%s: convert: %v", event, err)
		}
	}
	if converter.partial.Content[0].(TextContent).Text != "x" {
		t.Fatalf("text = %q", converter.partial.Content[0].(TextContent).Text)
	}
}
