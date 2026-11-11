package extensionconformance

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_CustomBlockedOption pins ui.custom's blocked option (Pi c1810bf7, types.ts ExtensionUIContext.custom): an inline component that
// passes {kind, message} reaches the host's UI with exactly that status, and a component without the option reaches it with none. A host
// that never read the option, or an SDK that dropped it, reports no status for the first call. The first call's kind and message are not
// any SDK's fallback.
func TestConformance_CustomBlockedOption(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
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
			var mu sync.Mutex
			var seen []string
			h.ui.runOverlay = func(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
				entry := "none"
				if opts.Blocked != nil {
					entry = string(opts.Blocked.Kind) + ":" + opts.Blocked.Message
				}
				mu.Lock()
				seen = append(seen, entry)
				mu.Unlock()
				handle := &conformanceOverlayHandle{closed: make(chan struct{}), changed: make(chan struct{}, 1)}
				onHandle(handle)
				host.OnInput("\r")
				select {
				case <-handle.closed:
					return handle.result, true
				case <-time.After(5 * time.Second):
					return nil, false
				}
			}
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "blocked-custom-probe")
			pollUntilConformance(t, "blocked-custom-probe never reported", func() bool {
				return slices.Contains(h.ui.Recorded(), "blocked-custom done:info")
			})
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"permission:Approve?", "none"}; !slices.Equal(seen, want) {
				t.Errorf("%s: the host saw blocked options %s, want %s", tc.name, strings.Join(seen, ", "), strings.Join(want, ", "))
			}
		})
	}
}
