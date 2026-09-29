package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// .upstream/v0.87.1/packages/coding-agent/test/cache-warmer.test.ts:339
func TestCacheWarmingDecisionUsesLastExtensionOverride(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	extensions := []extension.Extension{}
	for _, action := range []string{"warm", "stop"} {
		extensions = append(extensions, extension.Extension{Path: action, Handlers: map[string][]extension.HandlerFn{"cache_warming_decision": {func(...any) (any, error) {
			return &extension.CacheWarmingDecisionEventResult{Action: new(extension.CacheWarmingAction(action))}, nil
		}}}})
	}
	h.session.ReplaceRunner(inproc.NewRunner(extensions, t.TempDir()))
	event := icodingagent.CacheWarmingDecisionEvent{Type: "cache_warming_decision", WarmCost: 0.05, MissCost: 0.5, ContinuationProbability: 0.15, Action: icodingagent.CacheWarmingActionWarm}
	got, err := h.session.decideCacheWarming(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if got != icodingagent.CacheWarmingActionStop {
		t.Fatalf("action = %q, want stop", got)
	}
}
