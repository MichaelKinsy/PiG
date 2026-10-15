package extensionconformance

import (
	"context"
	"strings"
	"testing"
)

// TestConformance_TypedModelInfoAgreesBetweenGoAndRust: the host answers getModelInfo with Pi's Model (packages/ai/src/types.ts): maxTokens for the output limit and a cost object with input, output, cacheRead and cacheWrite
// (per million tokens). The Go SDK's ModelInfo reads those; the Rust SDK's get_model_info read only the keys maxOutputTokens and inputCostPer1M..., so it reported 0 for the output limit and for every cost: a constant, not an error.
func TestConformance_TypedModelInfoAgreesBetweenGoAndRust(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const want = "current/Current/conformance/321000/1234/false/0/7/0.5/1.5"
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if !strings.Contains(tc.name, "go") && !strings.Contains(tc.name, "rust") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "model-info-probe", "") {
				t.Fatal("fixture has no model-info-probe command")
			}
			var got string
			pollUntilConformance(t, "model-info-probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "modelinfo:"); ok {
						got = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			if got != want {
				t.Fatalf("typed model = %s\nwant         %s", got, want)
			}
		})
	}
}
