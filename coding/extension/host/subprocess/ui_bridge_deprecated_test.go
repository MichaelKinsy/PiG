package subprocess

import (
	"encoding/json"
	"testing"
)

// HostCallbacks.GetBranch and GetEntries and their SetHostAction keys were published in v0.2.0. They stay as deprecated aliases: setting them neither panics nor makes the host serve a getBranch or getEntries call, which no extension makes.
func TestDeprecatedBranchAndEntriesHostActionsAreAccepted(t *testing.T) {
	bridge := NewUIBridge(func() {})
	entries := func() []json.RawMessage { return []json.RawMessage{json.RawMessage(`{"type":"message"}`)} }
	bridge.SetHostAction("getBranch", entries)
	bridge.SetHostAction("getEntries", entries)
	actions := bridge.hostCallbacks()
	if actions == nil || actions.GetBranch == nil || actions.GetEntries == nil {
		t.Fatalf("deprecated keys did not set their fields: %+v", actions)
	}
	for _, method := range []string{"getBranch", "getEntries"} {
		result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: method})
		if err != nil || result == nil || result.Error == nil || result.Error.Code != "unknown_method" {
			t.Fatalf("%s call = %+v, %v; want unknown_method", method, result, err)
		}
	}
}
