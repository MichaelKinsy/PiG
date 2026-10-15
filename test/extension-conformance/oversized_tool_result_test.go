package extensionconformance

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_OversizedToolResultIsAnsweredAcrossSDKs: the wire refuses a frame past 128 MiB in every SDK. The host waits for each request's response, so a result that cannot be framed must be answered
// with that failure; the Node and Python SDKs did so, while the Go SDK discarded the failed send and the Rust SDK returned it to a caller that dropped it, so the call hung until the run was aborted. Each
// SDK must report "frame too large" and keep serving: the call after it succeeds. Encoding 128 MiB takes seconds, longer on a loaded host, so it runs under the shared test budget (internal/testbudget): a hang ends at it with "Operation aborted".
func TestConformance_OversizedToolResultIsAnsweredAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	const oversized = 128*1024*1024 + 1
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			tool, ok := findTool(h.runner, "big_out")
			if !ok {
				t.Fatal("big_out not registered")
			}
			run := func(n int) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
				defer cancel()
				result, err := tool.Definition.Execute(ctx, "tc-big", json.RawMessage(`{"n":`+strconv.Itoa(n)+`}`), nil)
				return result.Text(), err
			}
			start := time.Now()
			if _, err := run(oversized); err == nil || !strings.Contains(err.Error(), "frame too large") {
				t.Fatalf("a %d byte result after %v: error = %v, want the frame-too-large failure", oversized, time.Since(start).Round(time.Millisecond), err)
			}
			if text, err := run(1000); err != nil || len(text) != 1000 {
				t.Fatalf("the call after the oversized result: %d bytes, %v; the extension stopped serving", len(text), err)
			}
		})
	}
}
