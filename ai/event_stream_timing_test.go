package ai

import (
	"testing"
	"testing/synctest"
	"time"
)

func timingMessage(timestamp int64, durationMs *int64) *AssistantMessage {
	return &AssistantMessage{Content: []AssistantContentBlock{}, API: "openai-responses", Provider: "openai", Model: "m", StopReason: StopReasonStop, Timestamp: timestamp, DurationMs: durationMs}
}

func pushDone(t *testing.T, stream *AssistantMessageEventStream, message *AssistantMessage) {
	t.Helper()
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil {
		t.Fatal(err)
	}
}

// .upstream/v1.1.0/packages/ai/test/event-stream.test.ts "AssistantMessageEventStream timing" (#10549). The clock is synthetic (synctest), so the 20 ms the
// test waits is the measured duration exactly; upstream bounds it by `>= 15` because its real clock jitters.
func TestAssistantMessageEventStreamTimingUpstream(t *testing.T) {
	// event-stream.test.ts:117
	t.Run("sets durationMs on the final done or error message of a response it saw start", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := NewAssistantMessageEventStream()
			answer := timingMessage(time.Now().UnixMilli(), nil)
			time.Sleep(20 * time.Millisecond)
			pushDone(t, done, answer)
			if answer.DurationMs == nil || *answer.DurationMs != 20 {
				t.Fatalf("done durationMs = %v, want 20", answer.DurationMs)
			}
			if result := done.Result(); result.DurationMs == nil || *result.DurationMs != *answer.DurationMs {
				t.Fatalf("result durationMs = %v, want %d", result.DurationMs, *answer.DurationMs)
			}

			failed := NewAssistantMessageEventStream()
			failure := timingMessage(time.Now().UnixMilli(), nil)
			failure.StopReason = StopReasonError
			if err := failed.Push(ErrorEvent{Reason: StopReasonError, Error: failure}); err != nil {
				t.Fatal(err)
			}
			if failure.DurationMs == nil || *failure.DurationMs != 0 {
				t.Fatalf("error durationMs = %v, want 0", failure.DurationMs)
			}

			ended := NewAssistantMessageEventStream()
			result := timingMessage(time.Now().UnixMilli(), nil)
			time.Sleep(5 * time.Millisecond)
			ended.End(result)
			if result.DurationMs == nil || *result.DurationMs != 5 {
				t.Fatalf("end(result) durationMs = %v, want 5", result.DurationMs)
			}
		})
	})
	// event-stream.test.ts:139
	t.Run("keeps an existing duration, so a forwarding stream keeps the inner measurement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			outer := NewAssistantMessageEventStream()
			time.Sleep(20 * time.Millisecond)
			inner := NewAssistantMessageEventStream()
			answer := timingMessage(time.Now().UnixMilli(), nil)
			pushDone(t, inner, answer)
			measured := *answer.DurationMs
			pushDone(t, outer, answer)
			if *answer.DurationMs != measured || measured >= 20 {
				t.Fatalf("durationMs = %d after forwarding, inner measured %d (want < 20)", *answer.DurationMs, measured)
			}

			preset := timingMessage(time.Now().UnixMilli(), new(int64(1234)))
			pushDone(t, NewAssistantMessageEventStream(), preset)
			if *preset.DurationMs != 1234 {
				t.Fatalf("preset durationMs = %d, want 1234", *preset.DurationMs)
			}
		})
	})
	// event-stream.test.ts:156
	t.Run("leaves a message untimed when it started before the stream, such as a fetched deferred result", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fetched := timingMessage(time.Now().UnixMilli()-60_000, nil)
			pushDone(t, NewAssistantMessageEventStream(), fetched)
			if fetched.DurationMs != nil {
				t.Fatalf("durationMs = %d, want none", *fetched.DurationMs)
			}
		})
	})
	// event-stream.test.ts:163
	t.Run("does not time a message pushed after the stream completed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := NewAssistantMessageEventStream()
			pushDone(t, stream, timingMessage(time.Now().UnixMilli(), nil))
			late := timingMessage(time.Now().UnixMilli(), nil)
			pushDone(t, stream, late)
			if late.DurationMs != nil {
				t.Fatalf("late durationMs = %d, want none", *late.DurationMs)
			}
		})
	})
	// Math.round: a duration of 1.6 ms is 2 ms, not 1.
	t.Run("rounds to the nearest millisecond", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := NewAssistantMessageEventStream()
			message := timingMessage(time.Now().UnixMilli(), nil)
			time.Sleep(1600 * time.Microsecond)
			pushDone(t, stream, message)
			if *message.DurationMs != 2 {
				t.Fatalf("durationMs = %d, want 2", *message.DurationMs)
			}
		})
	})
}
