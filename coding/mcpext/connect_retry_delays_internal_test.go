package mcpext

import (
	"slices"
	"testing"
	"time"
)

// runtime.ts:51 CONNECT_RETRY_DELAYS_MS = [250, 1_000]: the waits before the two connection retries, and runtime.ts:304
// waits the first of them before retrying a read-only request. The behavioural tests can bound the waits only from below,
// so the values are pinned here.
func TestConnectRetryDelaysAreUpstreamsLiterals(t *testing.T) {
	if want := []time.Duration{250 * time.Millisecond, time.Second}; !slices.Equal(connectRetryDelays, want) {
		t.Fatalf("connectRetryDelays = %v, want %v", connectRetryDelays, want)
	}
}
