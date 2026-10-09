package codingagent

import "testing"

// packages/coding-agent/src/modes/interactive/components/footer.ts:73 setSession replaces the session the footer reads on every render
// (interactive-mode.ts:2069 applyRuntimeSettings calls it after the runtime replaces the session): the next frame shows the new session's cost
// and routed model, and a nil session shows neither.
func TestFooterSetSessionRebindsTheTotalsAndRoutedModelItReads(t *testing.T) {
	footer := upstreamFooter(t, nil, "test", "auto")
	footer.SetSession(testFooterSession{totals: func() footerUsageTotals { return footerUsageTotals{cost: 0.5} }})
	assertUpstreamFooterStats(t, footer, "$0.500")

	footer.SetSession(testFooterSession{totals: func() footerUsageTotals { return footerUsageTotals{cost: 2.25} }})
	assertUpstreamFooterStats(t, footer, "$2.250")

	footer.SetSession(nil)
	for _, line := range footer.Render(120) {
		if containsCost(line) {
			t.Fatalf("a footer with no session still shows a cost: %q", line)
		}
	}
}

func containsCost(line string) bool {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '$' && line[i+1] >= '0' && line[i+1] <= '9' {
			return true
		}
	}
	return false
}
