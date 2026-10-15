package ai

import "testing"

// pi: packages/ai/src/api/google-vertex.ts:212 — the Vertex module names itself in the message of a stream that ends without a finish reason.
func TestGoogleFinishReasonMessageNamesTheAPI(t *testing.T) {
	for api, want := range map[API]string{
		APIGoogleGenerativeAI: "Google stream ended without a finish reason",
		APIGoogleVertex:       "Google Vertex stream ended without a finish reason",
	} {
		if reason, message := mapGoogleFinishReason(api, ""); reason != StopReasonError || message != want {
			t.Errorf("%s: %v %q", api, reason, message)
		}
	}
}
