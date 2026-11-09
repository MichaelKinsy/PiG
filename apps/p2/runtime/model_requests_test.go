package runtime

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// sameResponse tells the copy of a stream's result the Harness hands an `afterResponse` hook from any other response (Pi
// keys requests by the message object itself; see model_requests.go).
func TestSameResponseIsTheCopyOfTheStreamsMessage(t *testing.T) {
	result := &ai.AssistantMessage{Timestamp: 10, ResponseID: "r1", StopReason: ai.StopReasonStop, DurationMs: new(int64(5))}
	copied := *result
	copied.ThinkingLevel = "high"
	if !sameResponse(result, copied) {
		t.Error("a copy of the result is not refused")
	}
	// The same numbers behind another allocation are another response.
	other := *result
	other.DurationMs = new(int64(5))
	if sameResponse(result, other) {
		t.Error("another response with equal fields is taken for the result")
	}
	// A response fetched after a deferred request carries no measured duration of the request's.
	fetched := ai.AssistantMessage{Timestamp: 10, ResponseID: "r1", StopReason: ai.StopReasonStop}
	if sameResponse(result, fetched) {
		t.Error("a fetched response is taken for the request's result")
	}
	// Without a measured duration the provider's own report tells them apart.
	plain := &ai.AssistantMessage{Timestamp: 10, ResponseID: "r1", StopReason: ai.StopReasonStop}
	same, differs := *plain, *plain
	differs.ResponseID = "r2"
	if !sameResponse(plain, same) || sameResponse(plain, differs) {
		t.Error("responses without a duration are not told apart by what the provider reported")
	}
}
