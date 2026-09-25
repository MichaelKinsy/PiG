package codingagent

import "testing"

// TestDefaultThinkingLevelMatchesUpstream is a tripwire: if upstream's
// defaults.ts changes its DEFAULT_THINKING_LEVEL value, this test
// fails and forces a conscious re-sync.
//
// Upstream source: .upstream/current/packages/coding-agent/src/core/defaults.ts
func TestDefaultThinkingLevelMatchesUpstream(t *testing.T) {
	if DefaultThinkingLevel != "medium" {
		t.Fatalf("DefaultThinkingLevel drift: got %q, upstream defaults.ts is %q", DefaultThinkingLevel, "medium")
	}
}
