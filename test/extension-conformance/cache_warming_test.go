package extensionconformance

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// packages/coding-agent/src/core/extensions/types.ts:1589 (pi.on cache_warming_decision): the handler's stop replaces the default warm in every SDK.
func TestCacheWarmingDecisionAcrossSDKs(t *testing.T) {
	for _, tc := range allHarnessCases() {
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
			event := extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: extension.CacheWarmingActionWarm}
			action, err := h.runner.EmitCacheWarmingDecision(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			if action != extension.CacheWarmingActionStop {
				t.Fatalf("action = %q; want stop, not the default warm", action)
			}
		})
	}
}
