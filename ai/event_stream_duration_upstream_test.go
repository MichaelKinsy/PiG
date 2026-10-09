package ai

import (
	"testing"
	"time"
)

func timedMessage(timestamp int64, durationMs *int64) *AssistantMessage {
	return &AssistantMessage{API: "openai-responses", Provider: "openai", Model: "m", StopReason: StopReasonStop, Timestamp: timestamp, DurationMs: durationMs}
}

// upstream: packages/ai/test/event-stream.test.ts "AssistantMessageEventStream timing" (#10549).
func TestAssistantMessageEventStreamTimesTheFinalMessage(t *testing.T) {
	t.Run("sets durationMs on the final done or error message of a response it saw start", func(t *testing.T) {
		done := NewAssistantMessageEventStream()
		answer := timedMessage(time.Now().UnixMilli(), nil)
		time.Sleep(20 * time.Millisecond)
		if err := done.Push(DoneEvent{Reason: StopReasonStop, Message: answer}); err != nil {
			t.Fatal(err)
		}
		if answer.DurationMs == nil || *answer.DurationMs < 15 {
			t.Fatalf("done durationMs = %v, want at least 15", answer.DurationMs)
		}
		if result := done.Result(); result.DurationMs == nil || *result.DurationMs != *answer.DurationMs {
			t.Fatalf("result durationMs = %v, want %d", result.DurationMs, *answer.DurationMs)
		}

		failed := NewAssistantMessageEventStream()
		failure := timedMessage(time.Now().UnixMilli(), nil)
		failure.StopReason = StopReasonError
		if err := failed.Push(ErrorEvent{Reason: StopReasonError, Error: failure}); err != nil {
			t.Fatal(err)
		}
		if failure.DurationMs == nil || *failure.DurationMs < 0 {
			t.Fatalf("error durationMs = %v", failure.DurationMs)
		}

		ended := NewAssistantMessageEventStream()
		result := timedMessage(time.Now().UnixMilli(), nil)
		ended.End(result)
		if result.DurationMs == nil || *result.DurationMs < 0 {
			t.Fatalf("End result durationMs = %v", result.DurationMs)
		}
	})
	t.Run("keeps an existing duration, so a forwarding stream keeps the inner measurement", func(t *testing.T) {
		outer := NewAssistantMessageEventStream()
		time.Sleep(20 * time.Millisecond)
		inner := NewAssistantMessageEventStream()
		answer := timedMessage(time.Now().UnixMilli(), nil)
		_ = inner.Push(DoneEvent{Reason: StopReasonStop, Message: answer})
		measured := *answer.DurationMs
		_ = outer.Push(DoneEvent{Reason: StopReasonStop, Message: answer})
		if *answer.DurationMs != measured || measured >= 20 {
			t.Fatalf("durationMs = %d after forwarding, inner measured %d (want under 20)", *answer.DurationMs, measured)
		}
		preset := timedMessage(time.Now().UnixMilli(), new(int64(1234)))
		_ = NewAssistantMessageEventStream().Push(DoneEvent{Reason: StopReasonStop, Message: preset})
		if *preset.DurationMs != 1234 {
			t.Fatalf("preset durationMs = %d, want 1234", *preset.DurationMs)
		}
	})
	t.Run("leaves a message untimed when it started before the stream, such as a fetched deferred result", func(t *testing.T) {
		fetched := timedMessage(time.Now().UnixMilli()-60_000, nil)
		_ = NewAssistantMessageEventStream().Push(DoneEvent{Reason: StopReasonStop, Message: fetched})
		if fetched.DurationMs != nil {
			t.Fatalf("durationMs = %d, want none", *fetched.DurationMs)
		}
	})
	t.Run("does not time a message pushed after the stream completed", func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: timedMessage(time.Now().UnixMilli(), nil)})
		late := timedMessage(time.Now().UnixMilli(), nil)
		_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: late})
		if late.DurationMs != nil {
			t.Fatalf("durationMs = %d, want none", *late.DurationMs)
		}
		afterEnd := timedMessage(time.Now().UnixMilli(), nil)
		stream.End(afterEnd)
		if afterEnd.DurationMs != nil {
			t.Fatalf("End after completion: durationMs = %d, want none", *afterEnd.DurationMs)
		}
	})
}
