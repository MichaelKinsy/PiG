package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/footer.ts

// pi: packages/coding-agent/src/core/footer-data-provider.ts

import (
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/footer.ts:68-71 constructor(session, footerData) and
// core/footer-data-provider.ts: the footer reads the git branch, extension statuses and provider count from the provider it was built with, on each
// render, and the usage totals from its session; two footers share one provider's data.
func TestNewFooterComponentForSessionReadsItsProviderAndSession(t *testing.T) {
	data := NewFooterDataProvider()
	data.SetExtensionStatus("ext", "building")
	data.SetProviderCount(2)
	session := testFooterSession{totals: func() footerUsageTotals { return footerUsageTotals{cost: 0.5} }}
	first := NewFooterComponentForSession(session, data)
	second := NewFooterComponentForSession(session, data)
	for name, footer := range map[string]*FooterComponent{"first": first, "second": second} {
		if footer.FooterDataProvider != data {
			t.Fatalf("%s footer does not share the provider", name)
		}
		assertUpstreamFooterStats(t, footer, "$0.500")
		out := strings.Join(footer.Render(120), "\n")
		if !strings.Contains(out, "building") {
			t.Errorf("%s footer misses the provider's extension status:\n%s", name, out)
		}
	}
	data.SetExtensionStatus("ext", "")
	if out := strings.Join(first.Render(120), "\n"); strings.Contains(out, "building") {
		t.Errorf("a cleared status still renders:\n%s", out)
	}
	if got := first.ProviderCount(); got != 2 {
		t.Errorf("ProviderCount = %d, want the provider's 2", got)
	}
	if footer := NewFooterComponentForSession(session, nil); footer.FooterDataProvider == nil {
		t.Error("a nil provider left the footer without one")
	}
}
