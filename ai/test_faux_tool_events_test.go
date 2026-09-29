package ai

import (
	"fmt"
	"slices"
	"testing"
)

// The paired Pi fixture emits start/delta/end for each tool before starting the next (test/parity/testdata/test-faux-provider.ts:576-587), like upstream providers/faux.ts:408-421. Ending only at done changes RPC's complete event sequence.
func TestTestFauxToolEventsEndBeforeNextCall(t *testing.T) {
	for _, tc := range []struct {
		prompt string
		tools  []string
	}{
		{prompt: "What is 20+22?"},
		{prompt: "Run: expr 20 + 22", tools: []string{"bash"}},
		{prompt: "Run: parallel reads", tools: []string{"read", "bash"}},
		{prompt: "Run: extension render cards", tools: []string{"render_card", "render_self", "render_throw", "render_fail"}},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			provider := &TestFauxProvider{}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(tc.prompt)}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got, want []string
			for event := range stream.Events(t.Context()) {
				switch event := event.(type) {
				case ToolCallStartEvent:
					got = append(got, fmt.Sprintf("start:%d", event.ContentIndex))
				case ToolCallDeltaEvent:
					got = append(got, fmt.Sprintf("delta:%d", event.ContentIndex))
				case ToolCallEndEvent:
					got = append(got, fmt.Sprintf("end:%d:%s", event.ContentIndex, event.ToolCall.Name))
				}
			}
			for i, name := range tc.tools {
				want = append(want, fmt.Sprintf("start:%d", i), fmt.Sprintf("delta:%d", i), fmt.Sprintf("end:%d:%s", i, name))
			}
			if !slices.Equal(got, want) {
				t.Fatalf("tool events = %v, want %v", got, want)
			}
			if result := stream.Result(); result.StopReason == StopReasonError {
				t.Fatal(result.ErrorMessage)
			}
		})
	}
}
