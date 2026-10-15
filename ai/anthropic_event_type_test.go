package ai

import (
	"encoding/json"
	"testing"
)

// iterateAnthropicEvents admits a record by its SSE event name, but the record's parsed type decides what it is: the message_start and
// message_stop flags (anthropic-messages.ts:552-556) and every branch of the provider loop (anthropic-messages.ts:665-815) compare
// event.type. Reading .type of a null record throws inside the parse try, so it is a parse error. Probed with Pi 1.1.0
// (scratch probe sse.sh with sse/ax-*.txt): PiG dispatched on the SSE name.
func TestAnthropicEventsDispatchOnTheRecordsTypeLikePi(t *testing.T) {
	record := func(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }
	start := record("message_start", `{"type":"message_start","message":{"id":"msg_type","usage":{"input_tokens":3,"output_tokens":0}}}`) +
		record("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	delta := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`
	end := record("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`) + record("message_stop", `{"type":"message_stop"}`)
	for _, test := range []struct {
		name, body, wantText, wantError string
		wantBlocks                      int
	}{
		{"a delta under another event name", start + record("message_delta", delta) + record("content_block_stop", `{"type":"content_block_stop","index":0}`) + end, "Hello", "", 1},
		{"the last type member wins", start + record("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"},"type":"ping"}`) + record("content_block_stop", `{"type":"content_block_stop","index":0}`) + end, "", "", 1},
		{"a repaired type that names nothing", record("message_start", `{"type":"message_start","message":{"id":"msg_type","usage":{"input_tokens":3,"output_tokens":0}}}`) + "event: content_block_start\ndata: {\"type\":\"content_blo\ndata: ck_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" + record("content_block_delta", delta) + end, "", "", 0},
		// The message_start/message_stop flags read the record's type too. Probed with Pi 1.1.0 (--mode json, retry off): a final record named
		// message_stop whose type is ping ends with "Anthropic stream ended before message_stop", and a message_stop type under message_delta
		// ends the stream cleanly.
		{"a message_stop name over another type", start + record("content_block_delta", delta) + record("content_block_stop", `{"type":"content_block_stop","index":0}`) + record("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`) + record("message_stop", `{"type":"ping"}`), "Hello", "Anthropic stream ended before message_stop", 1},
		{"a message_stop type under another name", start + record("content_block_delta", delta) + record("content_block_stop", `{"type":"content_block_stop","index":0}`) + record("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`) + record("message_delta", `{"type":"message_stop"}`), "Hello", "", 1},
		{"a record without an object", start + record("content_block_delta", delta) + record("content_block_stop", `5`) + end, "Hello", "", 1},
		{"a null record", start + record("content_block_delta", delta) + record("content_block_stop", `null`) + end, "Hello", `Could not parse Anthropic SSE event content_block_stop: Cannot read properties of null (reading 'type'); data=null; raw=event: content_block_stop\ndata: null`, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, events := runAnthropicWire(t, AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key"}, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}, StreamOptions{}, func(map[string]any) string { return test.body })
			var final AssistantMessage
			switch last := events[len(events)-1].(type) {
			case DoneEvent:
				final = *last.Message
			case ErrorEvent:
				final = *last.Error
			default:
				t.Fatalf("last event %T", last)
			}
			if final.ErrorMessage != test.wantError {
				t.Fatalf("errorMessage = %q, want %q", final.ErrorMessage, test.wantError)
			}
			if len(final.Content) != test.wantBlocks {
				t.Fatalf("content = %#v, want %d blocks", final.Content, test.wantBlocks)
			}
			if test.wantBlocks > 0 {
				if text, ok := final.Content[0].(TextContent); !ok || text.Text != test.wantText {
					t.Fatalf("content[0] = %#v, want text %q", final.Content[0], test.wantText)
				}
			}
		})
	}
}

// anthropicEventType must agree with JSON.parse(text).type compared as a string: last member wins, keys are exact and may be escaped.
func TestAnthropicEventTypeReadsTheLastTopLevelStringType(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{`{"type":"message_start","message":{"type":"message"}}`, "message_start"},
		{` { "message" : {"type":"x"} , "type" : "message_stop" } `, "message_stop"},
		{`{"type":"a","type":"b"}`, "b"},
		{`{"type":"a","type":5}`, ""},
		{`{"TYPE":"message_start"}`, ""},
		{`{"\u0074ype":"message_start"}`, "message_start"},
		{`{"type":"message_\u0073tart"}`, "message_start"},
		{`{"a":"}\"type\":","b":[1,{"type":"x"}],"type":"ok"}`, "ok"},
		{`{"type":null}`, ""},
		{`[{"type":"message_start"}]`, ""},
		{`"message_start"`, ""},
		{`5`, ""},
		{`{}`, ""},
	} {
		got, err := anthropicRecordType(test.text)
		if err != nil || got != test.want {
			t.Errorf("anthropicEventType(%s) = %q, %v; want %q", test.text, got, err, test.want)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(test.text), &object) == nil && object != nil {
			var want string
			_ = json.Unmarshal(object["type"], &want)
			if got != want {
				t.Errorf("anthropicEventType(%s) = %q, encoding/json reads %q", test.text, got, want)
			}
		}
	}
	if _, err := anthropicRecordType(` null`); err == nil || err.Error() != "Cannot read properties of null (reading 'type')" {
		t.Errorf("null record: %v", err)
	}
}
