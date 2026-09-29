package ai

import (
	"reflect"
	"testing"
)

// upstream: packages/ai/src/providers/faux.ts:339-355,423-435; packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:463-487
func TestFauxEmptyResponseLifecycle(t *testing.T) {
	t.Parallel()
	for _, reason := range []StopReason{StopReasonStop, StopReasonError, StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()
			provider := NewFauxProvider(FauxConfig{})
			t.Cleanup(func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			})
			provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{}, StopReason: string(reason), ErrorMessage: "failed", Timestamp: new(int64(20))})})
			stream, err := provider.Stream(t.Context(), emptyTranscript(), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var types []AssistantEventType
			for event := range stream.Events(t.Context()) {
				types = append(types, event.EventType())
				if start, ok := event.(StartEvent); ok {
					if start.Partial.StopReason != StopReasonPending || len(start.Partial.Content) != 0 || start.Partial.Timestamp != 20 {
						t.Errorf("start partial = %+v", start.Partial)
					}
				}
			}
			terminal := EventError
			if reason == StopReasonStop {
				terminal = EventDone
			}
			if want := []AssistantEventType{EventStart, terminal}; !reflect.DeepEqual(types, want) {
				t.Errorf("events = %v, want %v", types, want)
			}
			result := stream.Result()
			if result.StopReason != reason || result.ErrorMessage != "failed" || result.Timestamp != 20 || len(result.Content) != 0 {
				t.Errorf("result = %+v", result)
			}
		})
	}
}

func BenchmarkFauxEmptyErrorResponse(b *testing.B) {
	provider := NewFauxProvider(FauxConfig{})
	transcript := emptyTranscript()
	for b.Loop() {
		provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{StopReason: "error", ErrorMessage: "failed"})})
		stream, err := provider.Stream(b.Context(), transcript, StreamOptions{})
		if err != nil {
			b.Fatal(err)
		}
		for range stream.Events(b.Context()) {
		}
	}
}
