package extensionconformance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestHostCallErrorSurfaceAcrossSDKs pins what an extension sees when the host fails a call that Pi's own caller sees fail (pi's navigateTree is a
// Promise that rejects with the Error it threw): the call's error carries the host's code and message in every SDK, each in its own idiom.
//
//	Go, Rust        the error's text, "navigate_error: boom"; neither has a typed code
//	Python          HostCallError: str() is "navigate_error: boom", .code "navigate_error", .message "boom"
//	Lisp            host-call-error: the printed report is "navigate_error: boom", host-call-error-code "navigate_error", host-call-error-message "boom"
//	Node            the rejected Error: message "navigate_error: boom", .code "navigate_error"
//
// The Lisp report was the bare message while the other SDKs wrote the code before it, so a handler that printed a failure showed the author the
// text only there.
func TestHostCallErrorSurfaceAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
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
			h.bridge.SetHostAction("navigateTree", func(context.Context, string, *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
				return extension.CancelledResult{}, errors.New("boom")
			})
			code := "-"
			if strings.Contains(tc.name, "python") || strings.Contains(tc.name, "lisp") || strings.Contains(tc.name, "node") {
				code = "navigate_error"
			}
			want := "host_error:navigate_error: boom|" + code + ":info"
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "host_error_probe")
			pollUntilConformance(t, "the SDK never reported the host error", func() bool {
				return slices.ContainsFunc(h.ui.Recorded(), func(s string) bool { return strings.HasPrefix(s, "host_error:") })
			})
			var got string
			for _, notice := range h.ui.Recorded() {
				if strings.HasPrefix(notice, "host_error:") {
					got = notice
				}
			}
			if got != want {
				t.Fatalf("the extension saw %q, want %q", got, want)
			}
		})
	}
}
