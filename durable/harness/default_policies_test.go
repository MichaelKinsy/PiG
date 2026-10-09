package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness/agent.ts:19-37 DEFAULT_RETRY_POLICY, DEFAULT_COMPACTION_POLICY and DEFAULT_PROGRESS_POLICY: the built-in values, which
// ResolveSettings applies when the harness settings give none.
func TestDefaultPoliciesMatchPi(t *testing.T) {
	if want := (durable.ConversationRetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxAgentDelayMs: new(60000)}); !reflect.DeepEqual(DefaultRetryPolicy, want) {
		t.Errorf("DefaultRetryPolicy = %+v, want %+v", DefaultRetryPolicy, want)
	}
	if want := (durable.CompactionPolicy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768}); DefaultCompactionPolicy != want {
		t.Errorf("DefaultCompactionPolicy = %+v, want %+v", DefaultCompactionPolicy, want)
	}
	if want := (durable.ProgressPolicy{PartialIntervalMs: 100, OutputIntervalMs: 100}); DefaultProgressPolicy != want {
		t.Errorf("DefaultProgressPolicy = %+v, want %+v", DefaultProgressPolicy, want)
	}
	resolved := ResolveSettings(nil)
	if !reflect.DeepEqual(resolved.Retry, DefaultRetryPolicy) || resolved.Compaction != DefaultCompactionPolicy || resolved.Progress != DefaultProgressPolicy {
		t.Errorf("ResolveSettings(nil) = %+v, want the default policies", resolved)
	}
}
