package ai

import (
	"testing"
	"time"
)

// A stream that completed does not time a result passed to End either: Pi's #time checks `this.done` for both push and end.
func TestAssistantMessageEventStreamDoesNotTimeAnEndResultAfterCompletion(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: timingMessage(time.Now().UnixMilli(), nil)}); err != nil {
		t.Fatal(err)
	}
	ended := timingMessage(time.Now().UnixMilli(), nil)
	stream.End(ended)
	if ended.DurationMs != nil {
		t.Fatalf("durationMs after End on a completed stream = %d, want it absent", *ended.DurationMs)
	}
}

// Through the faux provider, as with real Pi 1.1.0 (probed with the published pi-ai: a factory's fauxAssistantMessage is timed
// and appended after the provider's own keys; a message scripted with an older timestamp is not).
func TestFauxProviderResponsesAreTimedByTheirTimestamp(t *testing.T) {
	request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x"), Timestamp: 1}}})
	for name, step := range map[string]FauxResponseStep{
		"a factory builds the message after the stream started": FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (AssistantMessage, error) {
			return FauxAssistantMessage(FauxContentBlocks{FauxText("hi")}, FauxAssistantMessageOptions{}), nil
		}),
		"a scripted message created before the stream": FauxAssistantMessage(FauxContentBlocks{FauxText("hi")}, FauxAssistantMessageOptions{Timestamp: new(time.Now().UnixMilli() - 60_000)}),
	} {
		t.Run(name, func(t *testing.T) {
			p := NewFauxProvider(FauxConfig{})
			p.SetResponses([]FauxResponseStep{step})
			result := fauxUpstreamComplete(t, p, request, StreamOptions{})
			_, wantTimed := step.(FauxResponseFactory)
			if (result.DurationMs != nil) != wantTimed {
				t.Fatalf("durationMs = %v, want timed = %t", result.DurationMs, wantTimed)
			}
		})
	}
}
