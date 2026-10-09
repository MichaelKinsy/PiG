package ai

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

// piMessagesLongStream is the backend events of a long turn: thinking, text, and tool calls whose arguments stream in fragments.
func piMessagesLongStream(textDeltas, toolCalls, argDeltas, argLines int) []json.RawMessage {
	var events []json.RawMessage
	add := func(event PiMessagesEvent) {
		encoded, err := json.Marshal(event)
		if err != nil {
			panic(err)
		}
		events = append(events, encoded)
	}
	add(PiMessagesEvent{Type: PiMessagesEventStart})
	add(PiMessagesEvent{Type: PiMessagesEventThinkingStart, ContentIndex: 0})
	for i := range textDeltas / 10 {
		add(PiMessagesEvent{Type: PiMessagesEventThinkingDelta, ContentIndex: 0, Delta: "think " + strconv.Itoa(i) + " "})
	}
	add(PiMessagesEvent{Type: PiMessagesEventThinkingEnd, ContentIndex: 0, Content: "done thinking"})
	add(PiMessagesEvent{Type: PiMessagesEventTextStart, ContentIndex: 1})
	for i := range textDeltas {
		add(PiMessagesEvent{Type: PiMessagesEventTextDelta, ContentIndex: 1, Delta: "tok" + strconv.Itoa(i%97) + " "})
	}
	add(PiMessagesEvent{Type: PiMessagesEventTextEnd, ContentIndex: 1, Content: "final text"})
	for call := range toolCalls {
		index := 2 + call
		add(PiMessagesEvent{Type: PiMessagesEventToolcallStart, ContentIndex: index, ID: "call_" + strconv.Itoa(call), ToolName: "write"})
		for _, fragment := range toolArgumentFragments(call, argDeltas, argLines) {
			add(PiMessagesEvent{Type: PiMessagesEventToolcallDelta, ContentIndex: index, Delta: fragment})
		}
		add(PiMessagesEvent{Type: PiMessagesEventToolcallEnd, ContentIndex: index})
	}
	add(PiMessagesEvent{Type: PiMessagesEventDone, Reason: StopReasonToolUse})
	return events
}

// convertPiMessages runs the events through the converter and folds every converted event and the partial it carries into one digest.
func convertPiMessages(t testing.TB, events []json.RawMessage, hashPartials bool) string {
	t.Helper()
	converter := newPiMessagesEventConverter("test", "model")
	converter.partial.Timestamp = 1
	hash := sha256.New()
	for _, raw := range events {
		converted, err := converter.convert(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range converted {
			_, _ = fmt.Fprintf(hash, "%s|", event.EventType())
			if hashPartials {
				encoded, err := json.Marshal(converter.partial)
				if err != nil {
					t.Fatal(err)
				}
				hash.Write(encoded)
			}
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// The pi-messages converter builds the same partial after every event as it did before deltas accumulated in a buffer.
func TestPiMessagesConverterLongStreamOutputUnchanged(t *testing.T) {
	events := piMessagesLongStream(300, 3, 24, 30)
	const want = "f8acbd814493c804744a07422bb0d32460b46037b0a860da57cdd6fc7f513a01"
	if got := convertPiMessages(t, events, true); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func BenchmarkPiMessagesConverterLongStream(b *testing.B) {
	events := piMessagesLongStream(20_000, 10, 200, 200)
	b.ReportAllocs()
	for b.Loop() {
		convertPiMessages(b, events, false)
	}
}

// reducerLongStream is the compact frames of the same kind of turn.
func reducerLongStream(textDeltas, toolCalls, argDeltas, argLines int) []AssistantMessageFrame {
	frames := []AssistantMessageFrame{StartFrame{Partial: AssistantMessage{Content: []AssistantContentBlock{}, StopReason: StopReasonPending, Timestamp: 1}}, TextStartFrame{ContentIndex: 0}}
	for i := range textDeltas {
		frames = append(frames, TextDeltaFrame{ContentIndex: 0, Delta: "tok" + strconv.Itoa(i%97) + " "})
	}
	frames = append(frames, TextEndFrame{ContentIndex: 0, Content: "final"})
	for call := range toolCalls {
		index := 1 + call
		frames = append(frames, ToolCallStartFrame{ContentIndex: index, ToolCall: ToolCall{ID: "call_" + strconv.Itoa(call), Name: "write", Arguments: JsonObject{}}})
		for _, fragment := range toolArgumentFragments(call, argDeltas, argLines) {
			frames = append(frames, ToolCallDeltaFrame{ContentIndex: index, Delta: fragment})
		}
	}
	return frames
}

func reduceDigest(t testing.TB, frames []AssistantMessageFrame) string {
	t.Helper()
	message, err := ReduceAssistantMessageFrames(frames)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func TestReduceAssistantMessageFramesLongStreamOutputUnchanged(t *testing.T) {
	const want = "5369bfd4c9b728df1ad981af084c6ae978dbb3cb1157224c385917a575f04d10"
	if got := reduceDigest(t, reducerLongStream(300, 3, 24, 30)); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func BenchmarkReduceAssistantMessageFramesLongStream(b *testing.B) {
	frames := reducerLongStream(50_000, 20, 400, 400)
	b.ReportAllocs()
	for b.Loop() {
		reduceDigest(b, frames)
	}
}
