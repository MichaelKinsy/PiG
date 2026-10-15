package ai

import (
	"encoding/json"
	"testing"
)

// faux.ts fauxAssistantMessage (79-100) writes `errorMessage`, then `responseId`, then `timestamp`, after usage and stopReason, so the persisted JSON of an
// error message that records its responseId has the keys in that order.
//
// mutation-checked: putting responseId in the head of the struct, before usage, fails it.
func TestAssistantErrorFirstWritesResponseIDAfterErrorMessage(t *testing.T) {
	message := AssistantMessage{
		API: "faux", Provider: "faux", Model: "faux-1", StopReason: StopReasonError,
		ErrorMessage: "boom", ResponseID: "resp-1", Timestamp: 7, ErrorBeforeTimestamp: true,
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"role":"assistant","content":null,"api":"faux","provider":"faux","model":"faux-1","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","errorMessage":"boom","responseId":"resp-1","timestamp":7}`
	if string(encoded) != want {
		t.Fatalf("encoded\n  %s\nwant\n  %s", encoded, want)
	}
}
