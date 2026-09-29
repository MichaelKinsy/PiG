package codingagent

import "testing"

func BenchmarkSessionSelectorStatusLifecycle(b *testing.B) {
	selector := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, nil })
	defer selector.clearStatusMessage()
	b.ReportAllocs()
	for b.Loop() {
		selector.setStatusMessage("Failed to load sessions: benchmark", true, sessionSelectorLoadErrorTimeout)
		if lines := selector.Render(120); len(lines) == 0 {
			b.Fatal("empty selector render")
		}
		selector.clearStatusMessage()
	}
}
