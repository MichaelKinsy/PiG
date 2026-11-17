package extensionconformance

import "testing"

// TestConformance_InPlaceToolResultEditsFollowPi is the tool_result rows of runEventEditRows: a member a handler assigns on the event (content, details,
// isError) is what the next handler of the chain sees, an edit made to an object the caller handed over (a block of the content, a member of the details)
// is also in the caller's result, and a member assigned another value reaches the caller only through a result a handler returned, also when the handler
// throws after the edit, in every SDK (runner.ts emitToolResult, agent-session.ts _afterToolCall).
func TestConformance_InPlaceToolResultEditsFollowPi(t *testing.T) {
	runEventEditRows(t, "tool_result")
}
