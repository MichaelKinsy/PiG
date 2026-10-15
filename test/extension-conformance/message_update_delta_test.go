package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// updateEvent is a message_update the way the agent loop builds one from a partial message.
func updateEvent(message ai.AssistantMessage, assistantEvent func(*ai.AssistantMessage) any) extension.MessageUpdateEvent {
	partial := message
	return extension.MessageUpdateEvent{
		Type: "message_update",
		Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: "assistant", Content: partial.Content, API: partial.API, Provider: partial.Provider, ModelID: partial.Model,
			Usage: &partial.Usage, StopReason: partial.StopReason, Timestamp: partial.Timestamp, ErrorMessage: partial.ErrorMessage,
		}},
		AssistantMessageEvent: assistantEvent(&partial),
	}
}

// messageUpdateStream is a streamed answer with thinking, text and a write call: the events a model produces for a long file, with strings
// that grow past the length from which the host sends only the appended text, a block that is replaced, and a usage that changes at the end.
func messageUpdateStream() []extension.MessageUpdateEvent {
	base := ai.AssistantMessage{API: "openai-completions", Provider: "faux", Model: "m", Timestamp: 1700000000000, StopReason: ai.StopReasonToolUse}
	var events []extension.MessageUpdateEvent
	thinking, answer, file := "", "", ""
	for step := range 36 {
		message := base
		blocks := []ai.AssistantContentBlock{}
		var build func(*ai.AssistantMessage) any
		switch {
		case step < 10:
			thinking += fmt.Sprintf("reasoning %d about a delta äöü; ", step)
			blocks = append(blocks, ai.ThinkingContent{Thinking: thinking})
			build = func(p *ai.AssistantMessage) any {
				return ai.ThinkingDeltaEvent{ContentIndex: 0, Delta: "reasoning", Partial: p}
			}
		case step < 20:
			answer += fmt.Sprintf("answer %d of the stream; ", step)
			blocks = append(blocks, ai.ThinkingContent{Thinking: thinking}, ai.TextContent{Text: answer})
			build = func(p *ai.AssistantMessage) any {
				return ai.TextDeltaEvent{ContentIndex: 1, Delta: "answer", Partial: p}
			}
		default:
			file += fmt.Sprintf("export function step%d(input: number[]): number { return input.length; }\\n", step)
			call := ai.ToolCall{ID: "call_1", Name: "write"}
			call.SetStreamingArguments(`{"path": "src/big.ts", "content": "` + file)
			blocks = append(blocks, ai.ThinkingContent{Thinking: thinking}, ai.TextContent{Text: answer}, call)
			build = func(p *ai.AssistantMessage) any {
				return ai.ToolCallDeltaEvent{ContentIndex: 2, Delta: "export", Partial: p}
			}
		}
		message.Content = blocks
		if step == 35 {
			message.Usage = ai.Usage{Input: 120, Output: 340, TotalTokens: 460}
		}
		events = append(events, updateEvent(message, build))
	}
	return events
}

// TestConformance_MessageUpdateDeltas pins that an extension process receives, for every message_update of a stream, the object the whole payload would have been. The host sends the first payload whole and each later one as the operations that turn the previous one into it (EventStream, coding/extension/host/subprocess/protocol.go); each SDK applies them. A runtime that dropped a delta, applied one twice or kept a stale copy reports a payload that differs from the one the event encodes to: every event of the stream carries text no fallback can produce.
func TestConformance_MessageUpdateDeltas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	events := messageUpdateStream()
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx := context.Background()
			const prefix = "event_payload:message_update:"
			var failures []string
			removeListener := h.runner.AddErrorListener(func(err *extension.ExtensionError) { failures = append(failures, err.Error) })
			defer removeListener()
			*h.notify = (*h.notify)[:0]
			runConformanceCommandArgs(t, h, "event_payload_probe", "message_update")
			for index, event := range events {
				if _, err := h.runner.Emit(ctx, event); err != nil {
					t.Fatalf("emit message_update %d: %v", index, err)
				}
			}
			var reported []string
			pollUntilConformance(t, "the handler did not report every message_update", func() bool {
				reported = reported[:0]
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, prefix); ok {
						reported = append(reported, strings.TrimSuffix(rest, ":info"))
					}
				}
				return len(reported) >= len(events)
			})
			// A runtime that cannot apply a delta answers with an error, and the host recovers with a whole payload that the next handler receives: the handler that failed, not the probe, saw the broken stream.
			if len(failures) > 0 {
				t.Fatalf("a handler failed during the stream: %v", failures)
			}
			for index, event := range events {
				whole, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal([]byte(reported[index]), &got); err != nil {
					t.Fatalf("payload %d is not JSON: %v", index, err)
				}
				if err := json.Unmarshal(whole, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("payload %d = %s, want %s", index, reported[index], whole)
				}
			}
		})
	}
}
