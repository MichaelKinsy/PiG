package agent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func failedAssistant(failure *ai.FailureResponse) *AssistantMessage {
	return &AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: "429 rate limited", Timestamp: 1, FailureResponse: failure}
}

// D654: the failure response is in-process only: session files, RPC and JSON mode write the same bytes with and without it.
func TestAssistantMessageFailureResponseIsNotSerialized(t *testing.T) {
	failure := &ai.FailureResponse{Status: 429, Headers: map[string]string{"retry-after": "3"}, StreamStarted: true}
	plain, err := json.Marshal(AgentMessage{Assistant: failedAssistant(nil)})
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := json.Marshal(AgentMessage{Assistant: failedAssistant(failure)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, recorded) {
		t.Fatalf("JSON differs:\nrecorded %s\nplain    %s", recorded, plain)
	}
}

// D654: the decider reads the failure response from the agent message the session holds: LLMMessage and Observe carry it.
func TestAssistantMessageViewsCarryTheFailureResponse(t *testing.T) {
	failure := &ai.FailureResponse{Status: 429, Headers: map[string]string{"retry-after": "3"}}
	message := failedAssistant(failure)
	if got := message.LLMMessage().FailureResponse; got != failure {
		t.Fatalf("LLMMessage().FailureResponse = %v, want the recorded one", got)
	}
	if got := message.Observe().FailureResponse; got == nil || got.Status != 429 || got.Headers["retry-after"] != "3" {
		t.Fatalf("Observe().FailureResponse = %v, want status 429 and retry-after 3", got)
	}
}

// D654: a message_end replacement that does not name a failure response keeps the one of the message it replaces; one that does names its own.
func TestReplaceMessageInPlaceKeepsTheFailureResponse(t *testing.T) {
	recorded := &ai.FailureResponse{Status: 429, Headers: map[string]string{"retry-after": "3"}}
	target := failedAssistant(recorded)
	ReplaceMessageInPlace(AgentMessage{Assistant: target}, AgentMessage{Assistant: failedAssistant(nil)})
	if target.FailureResponse != recorded {
		t.Fatalf("FailureResponse = %v after a replacement that has none, want the recorded one", target.FailureResponse)
	}
	own := &ai.FailureResponse{Status: 503}
	ReplaceMessageInPlace(AgentMessage{Assistant: target}, AgentMessage{Assistant: failedAssistant(own)})
	if target.FailureResponse != own {
		t.Fatalf("FailureResponse = %v after a replacement that names one, want that one", target.FailureResponse)
	}
}
