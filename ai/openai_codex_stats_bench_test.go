package ai

import (
	"errors"
	"testing"
)

// BenchmarkCodexWebSocketStatsChanges is the change sites of the stats store with no observer, for -benchmem before and after the hook.
func BenchmarkCodexWebSocketStatsChanges(b *testing.B) {
	failure := errors.New("boom")
	b.ReportAllocs()
	for b.Loop() {
		recordCodexWebSocketFailure("bench", failure)
		recordCodexWebSocketSSEFallback("bench")
		ResetOpenAICodexWebSocketDebugStats("bench")
	}
}
