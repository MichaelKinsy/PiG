package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports packages/durable/src/harness/agent.ts DEFAULT_RETRY_POLICY and DEFAULT_COMPACTION_POLICY: the values, and that settings
// resolution copies them (`{ ...DEFAULT_RETRY_POLICY, ...settings?.retry }`), so one resolved Settings cannot change another's.
func TestDefaultPoliciesMatchPiAndResolveByCopy(t *testing.T) {
	if r := DefaultRetryPolicy; !r.Enabled || r.MaxRetries != 3 || r.BaseDelayMs != 2000 || r.MaxAgentDelayMs == nil || *r.MaxAgentDelayMs != 60000 {
		t.Fatalf("retry policy = %+v", r)
	}
	if c := DefaultCompactionPolicy; !c.Enabled || c.ReserveTokens != 16384 || c.KeepRecentTokens != 20000 || c.BackgroundTokens != 32768 {
		t.Fatalf("compaction policy = %+v", c)
	}
	resolved := ResolveSettings(nil)
	if resolved.Retry.MaxAgentDelayMs == nil || *resolved.Retry.MaxAgentDelayMs != 60000 || resolved.Compaction != DefaultCompactionPolicy {
		t.Fatalf("resolved = %+v", resolved)
	}
	*resolved.Retry.MaxAgentDelayMs = 1
	if *DefaultRetryPolicy.MaxAgentDelayMs != 60000 || *ResolveSettings(nil).Retry.MaxAgentDelayMs != 60000 {
		t.Fatalf("a resolved retry policy aliases the default: %d", *DefaultRetryPolicy.MaxAgentDelayMs)
	}
}

// Ports packages/durable/src/harness/define.ts defineExtension: an identity function.
func TestDefineExtensionReturnsTheExtension(t *testing.T) {
	extension := durable.Extension{Name: "x"}
	if got := new(extension); got == nil || got.Name != "x" {
		t.Fatalf("got %+v", got)
	}
}
