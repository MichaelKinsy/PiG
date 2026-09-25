package ai

// Ports .upstream/current/packages/ai/test/assistant-message-frame.test.ts.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func frameSeed() *AssistantMessage {
	return &AssistantMessage{
		Content:    []AssistantContentBlock{},
		API:        "test-api",
		Provider:   "test-provider",
		Model:      "test-model",
		StopReason: StopReasonPending,
		Timestamp:  1,
	}
}

func mustFrame(t *testing.T, encoder *AssistantMessageFrameEncoder, event AssistantMessageEvent) AssistantMessageFrame {
	t.Helper()
	frame, err := encoder.Encode(event)
	if err != nil {
		t.Fatalf("Encode(%s): %v", event.EventType(), err)
	}
	if frame == nil {
		t.Fatalf("expected %s event to produce a frame", event.EventType())
	}
	return frame
}

func encodeAll(t *testing.T, events []AssistantMessageEvent) []AssistantMessageFrame {
	t.Helper()
	encoder := &AssistantMessageFrameEncoder{}
	var frames []AssistantMessageFrame
	for _, event := range events {
		frame, err := encoder.Encode(event)
		if err != nil {
			t.Fatalf("Encode(%s): %v", event.EventType(), err)
		}
		if frame != nil {
			frames = append(frames, frame)
		}
	}
	return frames
}

func mustReduce(t *testing.T, frames []AssistantMessageFrame) *AssistantMessage {
	t.Helper()
	message, err := ReduceAssistantMessageFrames(frames)
	if err != nil {
		t.Fatalf("ReduceAssistantMessageFrames: %v", err)
	}
	if message == nil {
		t.Fatal("ReduceAssistantMessageFrames returned no message")
	}
	return message
}

// assertJSONEqual compares canonical JSON so decoded numbers (json.Number,
// float64) compare equal to Go literals.
func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantJSON, &wantValue); err != nil {
		t.Fatal(err)
	}
	gotCanonical, _ := json.Marshal(gotValue)
	wantCanonical, _ := json.Marshal(wantValue)
	if string(gotCanonical) != string(wantCanonical) {
		t.Fatalf("got  %s\nwant %s", gotCanonical, wantCanonical)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func TestAssistantMessageFrameUsesAuthoritativeTextEndContentAndSignature(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, TextContent{Text: "Hello "})
	frames = append(frames, mustFrame(t, encoder, TextStartEvent{ContentIndex: 0, Partial: partial}))
	partial.Content[0] = TextContent{Text: "Hello world", TextSignature: "sig-text"}
	frames = append(frames,
		mustFrame(t, encoder, TextDeltaEvent{ContentIndex: 0, Delta: "incorrect", Partial: partial}),
		mustFrame(t, encoder, TextEndEvent{ContentIndex: 0, Content: "Hello world", Partial: partial}),
	)

	assertJSONEqual(t, frames[len(frames)-1], map[string]any{"type": "text_end", "contentIndex": 0, "content": "Hello world", "textSignature": "sig-text"})
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "text", "text": "Hello world", "textSignature": "sig-text"}})
}

func TestAssistantMessageFramePreservesProviderThinkingLevelFromStart(t *testing.T) {
	partial := frameSeed()
	partial.ProviderThinkingLevel = "high"
	start := mustFrame(t, &AssistantMessageFrameEncoder{}, StartEvent{Partial: partial})
	if start.(StartFrame).Partial.ProviderThinkingLevel != "high" {
		t.Fatalf("start = %#v", start)
	}
	if got := mustReduce(t, []AssistantMessageFrame{start}).ProviderThinkingLevel; got != "high" {
		t.Fatalf("reduced providerThinkingLevel = %q", got)
	}
}

func TestAssistantMessageFramePreservesThinkingMetadataIncludingRedaction(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, ThinkingContent{Thinking: "[redacted]", ThinkingSignature: "encrypted-start", Redacted: true})
	frames = append(frames, mustFrame(t, encoder, ThinkingStartEvent{ContentIndex: 0, Partial: partial}))
	partial.Content[0] = ThinkingContent{Thinking: "[redacted]", ThinkingSignature: "encrypted-final", Redacted: true}
	frames = append(frames, mustFrame(t, encoder, ThinkingEndEvent{ContentIndex: 0, Content: "[redacted]", Partial: partial}))

	want := map[string]any{"type": "thinking_end", "contentIndex": 0, "content": "[redacted]", "thinkingSignature": "encrypted-final", "redacted": true}
	assertJSONEqual(t, frames[len(frames)-1], want)
	assertJSONEqual(t, mustReduce(t, frames).Content[0], map[string]any{"type": "thinking", "thinking": "[redacted]", "thinkingSignature": "encrypted-final", "redacted": true})
}

func TestAssistantMessageFrameParsesUnfinishedToolJSONAndUsesAuthoritativeEnd(t *testing.T) {
	initial := []AssistantMessageFrame{
		StartFrame{Partial: *frameSeed()},
		ToolCallStartFrame{ContentIndex: 0, ToolCall: ToolCall{ID: "initial-id", Name: "write", Arguments: JsonObject{}}},
		ToolCallDeltaFrame{ContentIndex: 0, Delta: `{"path":"READ`},
	}
	if got := mustReduce(t, initial).Content[0].(ToolCall); got.Arguments["path"] != "READ" {
		t.Fatalf("unfinished arguments = %#v", got.Arguments)
	}

	complete := append(append([]AssistantMessageFrame{}, initial...),
		ToolCallDeltaFrame{ContentIndex: 0, Delta: `ME.md","lines":[1,2]}`},
		ToolCallEndFrame{ContentIndex: 0, ID: "final-id", Name: "write_file", Arguments: JsonObject{"path": "final.md", "lines": []any{3}}, ThoughtSignature: "thought", Namespace: "files"},
	)
	assertJSONEqual(t, mustReduce(t, complete).Content[0], map[string]any{
		"type": "toolCall", "id": "final-id", "name": "write_file",
		"arguments":        map[string]any{"path": "final.md", "lines": []any{3}},
		"thoughtSignature": "thought", "namespace": "files",
	})
}

func TestAssistantMessageFrameRoundTripsOpenAIResponsesEndOnlyContent(t *testing.T) {
	events := `data: {"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[]}}

data: {"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":"final text","annotations":[]}]}}

data: {"type":"response.output_item.added","sequence_number":2,"output_index":1,"item":{"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":""}}

data: {"type":"response.output_item.done","sequence_number":3,"output_index":1,"item":{"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":"{\"query\":\"pi\"}"}}

data: {"type":"response.completed","sequence_number":4,"response":{"id":"response","status":"completed","output":[]}}

`
	result, streamed := runOpenAIResponsesEvents(t, events)
	frames := encodeAll(t, streamed)
	if len(result.Content) != 2 {
		t.Fatalf("result content = %#v", result.Content)
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, result.Content)
}

func TestAssistantMessageFrameReconcilesQueuedTextAgainstAdvancedLivePartial(t *testing.T) {
	partial := frameSeed()
	events := []AssistantMessageEvent{StartEvent{Partial: partial}}
	partial.Content = append(partial.Content, TextContent{})
	events = append(events, TextStartEvent{ContentIndex: 0, Partial: partial})
	for _, delta := range []string{"Hel", "lo", " ", "world"} {
		text := partial.Content[0].(TextContent)
		text.Text += delta
		partial.Content[0] = text
		events = append(events, TextDeltaEvent{ContentIndex: 0, Delta: delta, Partial: partial})
	}

	frames := encodeAll(t, events)
	if len(frames) != 2 || frames[0].FrameType() != FrameTypeStart || frames[1].FrameType() != FrameTypeTextStart {
		t.Fatalf("frames = %#v", frames)
	}
	if start := frames[0].(StartFrame); len(start.Partial.Content) != 0 || start.Partial.StopReason != StopReasonPending {
		t.Fatalf("start partial = %#v", start.Partial)
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "text", "text": "Hello world"}})
}

func TestAssistantMessageFrameTrimsOnlyCoveredPrefixInsideDelta(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, TextContent{Text: "Hel"})
	frames = append(frames, mustFrame(t, encoder, TextStartEvent{ContentIndex: 0, Partial: partial}))
	if frame, err := encoder.Encode(TextDeltaEvent{ContentIndex: 0, Delta: "He", Partial: partial}); err != nil || frame != nil {
		t.Fatalf("covered delta = %#v, %v", frame, err)
	}
	remainder := mustFrame(t, encoder, TextDeltaEvent{ContentIndex: 0, Delta: "llo", Partial: partial})
	frames = append(frames, remainder)

	if remainder != (TextDeltaFrame{ContentIndex: 0, Delta: "lo"}) {
		t.Fatalf("remainder = %#v", remainder)
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "text", "text": "Hello"}})
}

func TestAssistantMessageFrameCheckpointsQueuedToolJSONWithoutReplayingCoveredDeltas(t *testing.T) {
	partial := frameSeed()
	events := []AssistantMessageEvent{StartEvent{Partial: partial}}
	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "write", Arguments: JsonObject{}})
	events = append(events, ToolCallStartEvent{ContentIndex: 0, Partial: partial})
	partial.Content[0] = ToolCall{ID: "call", Name: "write", Arguments: JsonObject{"path": "README.md"}}
	events = append(events,
		ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"path":"READ`, Partial: partial},
		ToolCallDeltaEvent{ContentIndex: 0, Delta: `ME.md"}`, Partial: partial},
	)
	// Every queued event shares the live partial, which advanced before the
	// start event was consumed.
	frames := encodeAll(t, events)
	types := make([]AssistantMessageFrameType, 0, len(frames))
	for _, frame := range frames {
		types = append(types, frame.FrameType())
	}
	if strings.Join(frameTypeStrings(types), ",") != "start,toolcall_start,toolcall_checkpoint" {
		t.Fatalf("frame types = %v", types)
	}
	if last := frames[len(frames)-1]; last != (ToolCallCheckpointFrame{ContentIndex: 0, JSON: `{"path":"README.md"}`}) {
		t.Fatalf("checkpoint = %#v", last)
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "toolCall", "id": "call", "name": "write", "arguments": map[string]any{"path": "README.md"}}})
}

func frameTypeStrings(types []AssistantMessageFrameType) []string {
	out := make([]string, len(types))
	for i, frameType := range types {
		out[i] = string(frameType)
	}
	return out
}

func TestAssistantMessageFrameResumesLegacyGrammarToolJSONFromInitialArguments(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"input": "a"}})
	frames = append(frames, mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial}))
	partial.Content[0] = ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"input": "ab"}}
	frames = append(frames, mustFrame(t, encoder, ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"input":"ab`, Partial: partial}))
	partial.Content[0] = ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"input": "abc"}}
	frames = append(frames, mustFrame(t, encoder, ToolCallDeltaEvent{ContentIndex: 0, Delta: `c"}`, Partial: partial}))

	if frames[2] != (ToolCallCheckpointFrame{ContentIndex: 0, JSON: `{"input":"ab`}) || frames[3] != (ToolCallDeltaFrame{ContentIndex: 0, Delta: `c"}`}) {
		t.Fatalf("frames = %#v", frames[2:])
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "toolCall", "id": "call", "name": "bash", "arguments": map[string]any{"input": "abc"}}})
}

func TestAssistantMessageFrameStreamsToolJSONCompactlyFromEmptyStart(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{}})
	frames = append(frames, mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial}))
	partial.Content[0] = ToolCall{ID: "call", Name: "bash", Arguments: JsonObject{"command": "ls -la /tmp"}}
	frames = append(frames, mustFrame(t, encoder, ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"command":"ls -la /tmp"}`, Partial: partial}))

	if last := frames[len(frames)-1]; last != (ToolCallDeltaFrame{ContentIndex: 0, Delta: `{"command":"ls -la /tmp"}`}) {
		t.Fatalf("last = %#v", last)
	}
	if got := mustReduce(t, frames).Content[0].(ToolCall); got.Arguments["command"] != "ls -la /tmp" {
		t.Fatalf("arguments = %#v", got.Arguments)
	}
}

func TestAssistantMessageFrameAcceptsPreGenerationErrorButRejectsSuccessOrUpdateBeforeStart(t *testing.T) {
	failed := frameSeed()
	failed.StopReason = StopReasonError
	failed.ErrorMessage = "setup failed"
	if frame, err := (&AssistantMessageFrameEncoder{}).Encode(ErrorEvent{Reason: StopReasonError, Error: failed}); err != nil || frame != nil {
		t.Fatalf("pre-generation error = %#v, %v", frame, err)
	}

	completed := frameSeed()
	completed.StopReason = StopReasonStop
	_, err := (&AssistantMessageFrameEncoder{}).Encode(DoneEvent{Reason: StopReasonStop, Message: completed})
	assertErrorContains(t, err, "done event appears before start")
	_, err = (&AssistantMessageFrameEncoder{}).Encode(TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: frameSeed()})
	assertErrorContains(t, err, "text_delta event appears before start")
}

func TestAssistantMessageFrameTreatsEndSignatureMetadataIncludingAbsenceAsAuthoritative(t *testing.T) {
	// Go has no distinct undefined for signature strings or redacted: an
	// absent end-frame field is the zero value, which clears stale metadata.
	frames := []AssistantMessageFrame{
		StartFrame{Partial: *frameSeed()},
		TextStartFrame{ContentIndex: 0, Content: TextContent{TextSignature: "stale-text"}},
		TextEndFrame{ContentIndex: 0},
		ThinkingStartFrame{ContentIndex: 1, Content: ThinkingContent{ThinkingSignature: "stale-thinking", Redacted: true}},
		ThinkingEndFrame{ContentIndex: 1},
		ToolCallStartFrame{ContentIndex: 2, ToolCall: ToolCall{ID: "call", Name: "read", Arguments: JsonObject{}, ThoughtSignature: "stale-tool", Namespace: "stale-namespace"}},
		ToolCallEndFrame{ContentIndex: 2, ID: "call", Name: "read", Arguments: JsonObject{}},
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{
		map[string]any{"type": "text", "text": ""},
		map[string]any{"type": "thinking", "thinking": ""},
		map[string]any{"type": "toolCall", "id": "call", "name": "read", "arguments": map[string]any{}},
	})
}

func TestAssistantMessageFrameStoresAuthoritativeFinalArgumentsInToolCallEnd(t *testing.T) {
	partial := frameSeed()
	toolCall := ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{"path": "README.md"}, ThoughtSignature: "thought", Namespace: "files"}
	partial.Content = append(partial.Content, toolCall)
	encoder := &AssistantMessageFrameEncoder{}
	mustFrame(t, encoder, StartEvent{Partial: partial})
	mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial})
	end := mustFrame(t, encoder, ToolCallEndEvent{ContentIndex: 0, ToolCall: toolCall, Partial: partial})
	assertJSONEqual(t, end, map[string]any{
		"type": "toolcall_end", "contentIndex": 0, "id": "call-1", "name": "read",
		"arguments": map[string]any{"path": "README.md"}, "thoughtSignature": "thought", "namespace": "files",
	})
}

func TestAssistantMessageFrameWhitelistsPublicFieldsFromProviderPartials(t *testing.T) {
	partial := frameSeed()
	partial.Content = append(partial.Content,
		TextContent{Text: "visible", TextSignature: "text-sig"},
		ThinkingContent{Thinking: "reasoning", ThinkingSignature: "thinking-sig"},
		ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"value": 1}, ThoughtSignature: "tool-sig", Namespace: "tools"},
	)
	partial.ErrorMessage = "scratch"
	partial.RawStopReason = "scratch"
	partial.Deferred = &DeferredHandle{Data: map[string]any{"scratch": true}}
	encoder := &AssistantMessageFrameEncoder{}
	start := mustFrame(t, encoder, StartEvent{Partial: partial}).(StartFrame)
	mustFrame(t, encoder, TextStartEvent{ContentIndex: 0, Partial: partial})
	mustFrame(t, encoder, ThinkingStartEvent{ContentIndex: 1, Partial: partial})
	mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 2, Partial: partial})

	if len(start.Partial.Content) != 0 || start.Partial.ErrorMessage != "" || start.Partial.RawStopReason != "" || start.Partial.Deferred != nil {
		t.Fatalf("start partial carried non-start fields: %#v", start.Partial)
	}
}

func TestAssistantMessageFrameSupportsInterleavedStreamsByContentIndex(t *testing.T) {
	frames := []AssistantMessageFrame{
		StartFrame{Partial: *frameSeed()},
		TextStartFrame{ContentIndex: 0},
		ToolCallStartFrame{ContentIndex: 1, ToolCall: ToolCall{ID: "call", Name: "lookup", Arguments: JsonObject{}}},
		ThinkingStartFrame{ContentIndex: 2},
		TextDeltaFrame{ContentIndex: 0, Delta: "answer"},
		ToolCallDeltaFrame{ContentIndex: 1, Delta: `{"query":"pi"}`},
		ThinkingDeltaFrame{ContentIndex: 2, Delta: "check"},
		ToolCallEndFrame{ContentIndex: 1, ID: "call", Name: "lookup", Arguments: JsonObject{"query": "pi"}},
		TextEndFrame{ContentIndex: 0, Content: "answer"},
		ThinkingEndFrame{ContentIndex: 2, Content: "check"},
	}
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{
		map[string]any{"type": "text", "text": "answer"},
		map[string]any{"type": "toolCall", "id": "call", "name": "lookup", "arguments": map[string]any{"query": "pi"}},
		map[string]any{"type": "thinking", "thinking": "check"},
	})
}

func TestAssistantMessageFrameSnapshotsMutableEventDataAndKeepsReductionPure(t *testing.T) {
	partial := frameSeed()
	partial.Diagnostics = []AssistantMessageDiagnostic{{Type: "test", Timestamp: 2, Details: map[string]any{"value": "original"}}}
	encoder := &AssistantMessageFrameEncoder{}
	start := mustFrame(t, encoder, StartEvent{Partial: partial})
	partial.Diagnostics[0].Details["value"] = "mutated"
	partial.Usage.Cost.Total = 99

	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"nested": map[string]any{"value": "original"}}})
	toolStart := mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial})
	partial.Content[0].(ToolCall).Arguments["nested"].(map[string]any)["value"] = "mutated"

	reduced := mustReduce(t, []AssistantMessageFrame{start, toolStart})
	if reduced.Diagnostics[0].Details["value"] != "original" || reduced.Usage.Cost.Total != 0 {
		t.Fatalf("reduced start = %#v", reduced)
	}
	assertJSONEqual(t, reduced.Content[0].(ToolCall).Arguments, map[string]any{"nested": map[string]any{"value": "original"}})

	reduced.Content[0].(ToolCall).Arguments["nested"] = "changed-output"
	assertJSONEqual(t, toolStart.(ToolCallStartFrame).ToolCall.Arguments["nested"], map[string]any{"value": "original"})
}

func TestAssistantMessageFrameOmitsTerminalEvents(t *testing.T) {
	message := frameSeed()
	completed := &AssistantMessageFrameEncoder{}
	mustFrame(t, completed, StartEvent{Partial: message})
	message.StopReason = StopReasonStop
	if frame, err := completed.Encode(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil || frame != nil {
		t.Fatalf("done = %#v, %v", frame, err)
	}
	_, err := completed.Encode(TextDeltaEvent{ContentIndex: 0, Delta: "late", Partial: message})
	assertErrorContains(t, err, "follows a terminal event")
	message.StopReason = StopReasonError
	message.ErrorMessage = "failed"
	if frame, err := (&AssistantMessageFrameEncoder{}).Encode(ErrorEvent{Reason: StopReasonError, Error: message}); err != nil || frame != nil {
		t.Fatalf("error = %#v, %v", frame, err)
	}
}

func TestReduceAssistantMessageFramesReturnsNilWithoutStartFrame(t *testing.T) {
	for _, frames := range [][]AssistantMessageFrame{nil, {TextDeltaFrame{ContentIndex: 0, Delta: "x"}}} {
		message, err := ReduceAssistantMessageFrames(frames)
		if err != nil || message != nil {
			t.Fatalf("Reduce(%#v) = %#v, %v", frames, message, err)
		}
	}
}

func TestReduceAssistantMessageFramesRejectsInvalidSequences(t *testing.T) {
	tests := []struct {
		name   string
		frames []AssistantMessageFrame
		want   string
	}{
		{"frame before start", []AssistantMessageFrame{TextDeltaFrame{ContentIndex: 0, Delta: "x"}, StartFrame{Partial: *frameSeed()}}, "before the start frame"},
		{"wrong block kind", []AssistantMessageFrame{
			StartFrame{Partial: *frameSeed()},
			ToolCallStartFrame{ContentIndex: 0, ToolCall: ToolCall{ID: "call", Name: "run", Arguments: JsonObject{}}},
			TextDeltaFrame{ContentIndex: 0, Delta: "wrong"},
		}, "expected text block"},
		{"duplicate end", []AssistantMessageFrame{
			StartFrame{Partial: *frameSeed()},
			TextStartFrame{ContentIndex: 0},
			TextEndFrame{ContentIndex: 0},
			TextEndFrame{ContentIndex: 0},
		}, "follows the end"},
		{"index gap", []AssistantMessageFrame{StartFrame{Partial: *frameSeed()}, TextStartFrame{ContentIndex: 1}}, "would leave a gap"},
		{"second start", []AssistantMessageFrame{StartFrame{Partial: *frameSeed()}, StartFrame{Partial: *frameSeed()}}, "more than one start frame"},
		{"negative index", []AssistantMessageFrame{StartFrame{Partial: *frameSeed()}, TextStartFrame{ContentIndex: -1}}, "Invalid assistant message frame contentIndex"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ReduceAssistantMessageFrames(test.frames)
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestAssistantMessageFrameRejectsEventsPointingToWrongBlockKind(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	mustFrame(t, encoder, StartEvent{Partial: partial})
	partial.Content = append(partial.Content, ThinkingContent{})
	_, err := encoder.Encode(TextStartEvent{ContentIndex: 0, Partial: partial})
	assertErrorContains(t, err, "text_start event points to thinking block")
}

func TestAssistantMessageFrameRejectsNonJSONToolArguments(t *testing.T) {
	partial := frameSeed()
	encoder := &AssistantMessageFrameEncoder{}
	mustFrame(t, encoder, StartEvent{Partial: partial})
	partial.Content = append(partial.Content, ToolCall{ID: "call", Name: "run", Arguments: JsonObject{"value": math.NaN()}})
	_, err := encoder.Encode(ToolCallStartEvent{ContentIndex: 0, Partial: partial})
	assertErrorContains(t, err, "not JSON-serializable")
}

func TestAssistantMessageFrameJSONCarriesFrameType(t *testing.T) {
	assertJSONEqual(t, ToolCallCheckpointFrame{ContentIndex: 2, JSON: `{"a":1}`}, map[string]any{"type": "toolcall_checkpoint", "contentIndex": 2, "json": `{"a":1}`})
	assertJSONEqual(t, TextDeltaFrame{ContentIndex: 0, Delta: "x"}, map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x"})
}
