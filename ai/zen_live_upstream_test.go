//go:build live

package ai

import (
	"context"
	"testing"
	"time"
)

// .upstream/v0.87.1/packages/ai/test/zen.test.ts:15
// Live-only: whether the remote OpenCode service currently accepts every catalog model cannot be established by a faux endpoint. The matching hermetic test covers request construction and stream parsing for the same independent model denominator.
func TestOpenCodeModelsLiveUpstream(t *testing.T) {
	for _, tc := range upstreamOpenCodeCases(t) {
		label := "OpenCode Zen"
		if tc.Provider == "opencode-go" {
			label = "OpenCode Go"
		}
		t.Run(label+": "+tc.ID, func(t *testing.T) {
			key := liveProviderKey(t, "opencode")
			model, ok := LookupModelExact(tc.Provider + "/" + tc.ID)
			if !ok {
				t.Fatal("missing upstream model")
			}
			provider := newMatrixProviderWithKey(t, model, model.BaseURL, false, key)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Say hello."), Timestamp: time.Now().UnixMilli()}}}), StreamOptions{IsReasoning: model.Reasoning, ModelCost: model.ToModel().CostRates()})
			if err != nil {
				t.Fatal(err)
			}
			response := stream.Result()
			if response.Content == nil {
				t.Fatal("missing response content")
			}
			if response.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %s; error = %s", response.StopReason, response.ErrorMessage)
			}
		})
	}
}
