package ai

import "testing"

// pi: packages/ai/src/api/google-vertex.ts — the Vertex module names itself in the message of a stream that ends without a finish reason.
func TestGoogleFinishReasonMessageNamesTheAPI(t *testing.T) {
	for api, want := range map[API]string{
		APIGoogleGenerativeAI: "Google stream ended without a finish reason after receiving 0 chunks",
		APIGoogleVertex:       "Google Vertex stream ended without a finish reason after receiving 0 chunks",
	} {
		state := &googleStreamState{
			builder: &assistantStreamBuilder{partial: &AssistantMessage{API: api}},
		}
		if reason, message := state.resolveFinishReason(); reason != StopReasonError || message != want {
			t.Errorf("%s: %v %q, want %q", api, reason, message, want)
		}
	}
}
