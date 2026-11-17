package extensionconformance

import "testing"

// TestConformance_InPlaceContextEditsFollowPi is the context rows of runEventEditRows: a handler's in-place edit of a message of event.messages reaches the
// next handler and the request, also when the handler throws after it (Pi hands every handler the one set of message objects, runner.ts emitContext), in
// every SDK.
func TestConformance_InPlaceContextEditsFollowPi(t *testing.T) {
	runEventEditRows(t, "context")
}
