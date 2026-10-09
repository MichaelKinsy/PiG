package extensionconformance

import (
	"slices"
	"testing"
	"time"
)

// TestConformance_ExtensionUnboundSessionActions pins Pi's runner.ts:383-387,557-562: a command context whose actions the host never bound answers
// newSession, fork, navigateTree and switchSession with { cancelled: false }, and reload with a resolved no-op (`async () => {}`). Every SDK
// reaches the host through the bridge, which must answer the same way instead of failing the call with "not available".
func TestConformance_ExtensionUnboundSessionActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const want = "unbound:new=false,fork=false,navigate=false,switch=false,reload=ok:info"
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			*h.notify = nil
			runConformanceCommand(t, h, "session_actions_unbound")
			pollUntilConformance(t, 5*time.Second, "the SDK never reported its unbound session actions", func() bool {
				return slices.ContainsFunc(*h.notify, func(s string) bool { return len(s) > 8 && s[:8] == "unbound:" })
			})
			var got string
			for _, notice := range *h.notify {
				if len(notice) > 8 && notice[:8] == "unbound:" {
					got = notice
				}
			}
			if got != want {
				t.Fatalf("unbound session actions answered %q, want %q", got, want)
			}
		})
	}
}
