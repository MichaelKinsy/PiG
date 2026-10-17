package ai

import "testing"

// test-faux-provider.ts:531-535 sets the error state on `output`, then pushes `start` and `error` with that one object, and event-stream.ts stamps
// durationMs on it when the error is pushed. A consumer that reads the start event afterwards sees the stamped object, so the start partial carries the duration.
func TestTestFauxStartEventCarriesTheDurationStampedByALaterError(t *testing.T) {
	p := &TestFauxProvider{}
	stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Trigger: retryable provider error")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for _, event := range stream.ObserveEvents(t.Context()) {
		if start, ok := event.(StartEvent); ok {
			started = true
			if start.Partial.ShallowCopy().DurationMs == nil {
				t.Fatal("start partial has no durationMs after the error stamped the shared message")
			}
		}
	}
	if !started {
		t.Fatal("no start event")
	}
}
