package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestRecordThinkingLevelOfAnAvailableLevelAllocatesNothing pins that an agent run's end, which asks recordThinkingLevel whether the branch's
// recorded level is one the model supports, does not build the model's supported levels: a Session whose recorded level is the effective one
// appends nothing and allocates nothing, with a model that reasons and with none selected.
func TestRecordThinkingLevelOfAnAvailableLevelAllocatesNothing(t *testing.T) {
	reasoning := warmingModel(&warmingProvider{})
	reasoning.ProviderMeta.Reasoning = true
	for name, options := range map[string]SessionOptions{
		"no model":        {NoSession: true, SkipBuiltinTools: true},
		"reasoning model": {NoSession: true, SkipBuiltinTools: true, Model: reasoning, ThinkingLevel: ai.ThinkingHigh},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := NewSession(warmingServices(t, "idle"), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			branch := s.inner.Load().GetContextBranch()
			recorded, found := recordedThinkingLevel(branch)
			if !found {
				t.Fatal("the branch records no thinking level")
			}
			if recorded != s.ThinkingLevel() {
				t.Fatalf("recorded level %q, effective level %q: the call would append an entry", recorded, s.ThinkingLevel())
			}
			if got := testing.AllocsPerRun(100, func() { s.recordThinkingLevel(branch) }); got != 0 {
				t.Errorf("recordThinkingLevel allocates %v times, want 0", got)
			}
			if after := s.inner.Load().GetContextBranch(); len(after) != len(branch) {
				t.Errorf("the branch grew from %d to %d entries", len(branch), len(after))
			}
		})
	}
}
