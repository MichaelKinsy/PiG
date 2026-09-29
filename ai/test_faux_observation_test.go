package ai

import "testing"

// The paired test-faux-provider.ts emitPlan creates a distinct initial snapshot; this fixture contract is separate from native provider live references.
func TestTestFauxPlanStartIsIndependentSnapshot(t *testing.T) {
	for _, prompt := range []string{"What is 20+22?", "Run: read parity-read-target.txt", "Trigger: over-window response"} {
		t.Run(prompt, func(t *testing.T) {
			provider := &TestFauxProvider{}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(prompt)}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			for event := range stream.Events(t.Context()) {
				if start, ok := event.(StartEvent); ok {
					observed := start.Partial.Observe()
					if len(observed.Content) != 0 || observed.StopReason != StopReasonPending {
						t.Fatalf("fixture start followed the output reference: %#v", observed)
					}
					if prompt == "Trigger: over-window response" && observed.Usage.Input != result.Usage.Input {
						t.Fatalf("fixture start lost precomputed usage: start=%#v result=%#v", observed.Usage, result.Usage)
					}
				}
			}
		})
	}
}
