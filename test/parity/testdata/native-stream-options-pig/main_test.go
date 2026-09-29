package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestRequireCapturedPayload(t *testing.T) {
	captured := errors.New("captured before network")
	for _, tc := range []struct {
		name        string
		immediate   error
		reason      ai.StopReason
		message     string
		wantSuccess bool
	}{
		{name: "direct capture", immediate: captured, wantSuccess: true},
		{name: "wrapped capture", immediate: fmt.Errorf("onPayload: %w", captured), wantSuccess: true},
		{name: "unrelated setup failure", immediate: errors.New("missing credential")},
		{name: "missing result"},
		{name: "terminal capture", reason: ai.StopReasonError, message: captured.Error(), wantSuccess: true},
		{name: "unrelated terminal failure", reason: ai.StopReasonError, message: "network attempted"},
		{name: "abort is not capture", reason: ai.StopReasonAborted, message: captured.Error()},
		{name: "success is not capture", reason: ai.StopReasonStop, message: captured.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream *ai.AssistantMessageEventStream
			if tc.reason != "" {
				stream = ai.NewAssistantMessageEventStream()
				message := &ai.AssistantMessage{StopReason: tc.reason, ErrorMessage: tc.message}
				var event ai.AssistantMessageEvent = ai.ErrorEvent{Reason: tc.reason, Error: message}
				if tc.reason == ai.StopReasonStop {
					event = ai.DoneEvent{Reason: tc.reason, Message: message}
				}
				if err := stream.Push(event); err != nil {
					t.Fatal(err)
				}
			}
			if err := requireCapturedPayload(stream, tc.immediate, captured); (err == nil) != tc.wantSuccess {
				t.Fatalf("capture result error=%v, want success=%t", err, tc.wantSuccess)
			}
		})
	}
}
