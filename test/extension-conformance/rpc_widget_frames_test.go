package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's RPC mode forwards ctx.ui.setWidget for a string array and for a clear, and ignores a component factory without calling it (rpc-mode.ts
// setWidget). Every SDK's component widget reaches the host as a frame or a view (kit-kinds); in RPC mode none of them may reach the host's widget
// request observer or stay behind as a frame the RPC widget sync would forward, while the string list the same extension sets (surface_widget)
// still does. Isolated and packed realizations alike.
func TestConformance_RPCModeDropsFrameWidgetsInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	rpcRowMode = "rpc"
	t.Cleanup(func() { rpcRowMode = "" })
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
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
			var requests []string
			h.bridge.SetWidgetRequestFunc(func(_ string, key string, lines []string, _ *extension.ExtensionWidgetOptions) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, key+":"+strings.Join(lines, "|"))
			})
			h.host.NotifyWidth(60)
			// The component widget first; a command error is not the contract here, only what reaches the host.
			if cmd, ok := findCommand(h.runner, "kit-kinds"); ok {
				_ = cmd.Handler(context.Background(), "")
			}
			stringWidget := func() bool {
				mu.Lock()
				defer mu.Unlock()
				if slices.ContainsFunc(requests, func(r string) bool { return strings.HasPrefix(r, "wide:") && strings.Contains(r, "short") }) {
					return true
				}
				proxy := h.bridge.GetWidget(tc.extName, "wide")
				return proxy != nil && !proxy.IsFrame()
			}
			pollUntilConformance(t, "the string widget never reached the RPC host", func() bool {
				runConformanceCommand(t, h, "surface_widget")
				time.Sleep(50 * time.Millisecond)
				return stringWidget()
			})
			time.Sleep(300 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			for _, request := range requests {
				if strings.HasPrefix(request, "kit-kinds:") && request != "kit-kinds:" {
					t.Errorf("RPC mode forwarded a component widget: %q", request)
				}
			}
			if proxy := h.bridge.GetWidget(tc.extName, "kit-kinds"); proxy != nil && !proxy.IsFrame() {
				t.Errorf("the component widget is held as a string list, which RPC mode would forward")
			}
		})
	}
}
