package extensionconformance

import (
	"strings"
	"testing"
)

// TestConformance_InPlaceMessageEndEditsFollowPi is the message_end rows of runEventEditRows: a handler's in-place edit of event.message (a block's text, the
// content list) is the message the next handler, the caller and the entry hold, also when the handler throws after it, and assigning event.message
// changes nothing, in every SDK (runner.ts emitMessageEnd, agent-session.ts message_end).
func TestConformance_InPlaceMessageEndEditsFollowPi(t *testing.T) {
	runEventEditRows(t, "message_end")
}

// TestConformance_InPlaceMessageEndEditReachesTheTranscript pins that a message_end handler's in-place edit of the user's message is the message the next provider
// request carries and the entry the session saves, as in Pi, where agent-session.ts stores the one object the handlers edited (runner.ts emitMessageEnd,
// agent-session.ts appendMessage), in every SDK.
func TestConformance_InPlaceMessageEndEditReachesTheTranscript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

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
			runConformanceCommandArgs(t, h, "event_edit_probe", "message_end deep")
			recorded := sendRecordedRequest(t, h, t.TempDir(), newOpenAIProbeProvider)
			if !strings.Contains(string(recorded.body), inPlaceEdited) {
				t.Errorf("the provider request does not carry the edited user message: %s", recorded.body)
			}
			if !strings.Contains(recorded.entries, inPlaceEdited) {
				t.Errorf("the session's entries do not hold the edited user message: %s", recorded.entries)
			}
		})
	}
}
