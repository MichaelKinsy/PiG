package ai

import (
	"context"
	"testing"
)

// faux.ts createAbortedMessage (:344) stamps every aborted stream with stopReason "aborted" and errorMessage "Request was aborted", and the stream ends with an
// error event of reason "aborted" carrying that message (:367-370 before the first chunk, :391-394, :414-417, :433-436 mid-chunk), whichever block was streaming.
func TestFauxAbortedStreamsCarryPisAbortedMessage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block FauxContentBlock
		delta AssistantEventType
	}{
		{"before the first chunk", FauxText("abcdefghijklmnopqrstuvwxyz"), ""},
		{"text", FauxText("abcdefghijklmnopqrstuvwxyz"), EventTextDelta},
		{"thinking", FauxThinking("abcdefghijklmnopqrstuvwxyz"), EventThinkingDelta},
		{"toolcall", FauxToolCall("echo", map[string]any{"text": "abcdefghijklmnopqrstuvwxyz"}, &FauxToolCallOptions{ID: "tool-1"}), EventToolCallDelta},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewFauxProvider(FauxConfig{TokensPerSecond: 100, TokenSize: &FauxTokenSize{Min: new(3), Max: new(3)}})
			p.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{tc.block}, StopReason: "stop"})})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.delta == "" {
				cancel()
			}
			stream, err := p.Stream(ctx, fauxUpstreamRequest(), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var last AssistantMessageEvent
			for event := range stream.Events(t.Context()) {
				if event.EventType() == tc.delta {
					cancel()
				}
				last = event
			}
			result := stream.Result()
			errorEvent, ok := last.(ErrorEvent)
			if !ok || errorEvent.Reason != StopReasonAborted || errorEvent.Error.ErrorMessage != "Request was aborted" {
				t.Fatalf("last event = %#v, want an aborted error event carrying %q", last, "Request was aborted")
			}
			if result.StopReason != StopReasonAborted || result.ErrorMessage != "Request was aborted" {
				t.Fatalf("result = %v %q, want aborted with %q", result.StopReason, result.ErrorMessage, "Request was aborted")
			}
		})
	}
}
