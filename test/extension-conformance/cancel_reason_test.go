package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// The host names why it cancelled a request in the cancel frame (subprocess CancelPayload.Reason), and every SDK hands that
// reason to the handler: Python and Rust through cancellation_reason, Go through CancellationReason, and Node as the reason
// its request's AbortSignal aborts with (runtime.mjs aborts with env.cancel.reason). The row cancels a running abort_tool
// and reads the reason the tool saw; a SDK that drops the reason reports "". Node's tools receive Pi's own AbortSignal, so
// the row covers the native SDKs.
func TestCancelReasonReachesEveryNativeSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if strings.Contains(tc.name, "node") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			closeRegistryHarness(t, h)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			abortTool, ok := findTool(h.runner, "abort_tool")
			if !ok {
				t.Fatal("abort_tool not registered")
			}
			toolCtx, cancelTool := context.WithCancel(ctx)
			waiting := make(chan struct{})
			var once sync.Once
			var onWaiting agent.ToolUpdateCallback = func(agent.AgentToolResult) { once.Do(func() { close(waiting) }) }
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = abortTool.Definition.Execute(toolCtx, "tc-reason", json.RawMessage(`{}`), onWaiting)
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal("abort_tool never reported that it was waiting")
			}
			cancelTool()
			<-done
			// The host returns at cancellation; the extension observes it asynchronously, so probe until it reports. The
			// reason is the host's: the text of the cancelled context.
			waitFor(t, func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "abort_reason")
				return slices.Contains(h.ui.Recorded(), "reason:context canceled:info")
			})
		})
	}
}
