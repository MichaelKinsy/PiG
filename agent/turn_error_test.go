package agent_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func assistantMsg(reason ai.StopReason, message string) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:         "assistant",
		StopReason:   reason,
		ErrorMessage: message,
	}}
}

func TestTurnError(t *testing.T) {
	if err := agent.TurnError(nil); err != nil {
		t.Fatalf("no messages: got %v, want nil", err)
	}
	if err := agent.TurnError([]agent.AgentMessage{{User: &agent.UserMessage{}}}); err != nil {
		t.Fatalf("no assistant message: got %v, want nil", err)
	}
	if err := agent.TurnError([]agent.AgentMessage{assistantMsg(ai.StopReasonStop, "")}); err != nil {
		t.Fatalf("stop turn: got %v, want nil", err)
	}
	// A length-limit stop is a completed turn, not a failure.
	if err := agent.TurnError([]agent.AgentMessage{assistantMsg(ai.StopReasonLength, "")}); err != nil {
		t.Fatalf("length-limited turn: got %v, want nil", err)
	}
	// A cancellation is the caller's own control flow — Send already returns
	// the context error for it, so TurnError stays nil.
	if err := agent.TurnError([]agent.AgentMessage{assistantMsg(ai.StopReasonAborted, "")}); err != nil {
		t.Fatalf("aborted turn: got %v, want nil", err)
	}
	// Only the final assistant message decides: an earlier failed attempt the
	// loop recovered from is not the turn's outcome.
	if err := agent.TurnError([]agent.AgentMessage{
		assistantMsg(ai.StopReasonError, "transient"),
		assistantMsg(ai.StopReasonStop, ""),
	}); err != nil {
		t.Fatalf("recovered turn: got %v, want nil", err)
	}

	err := agent.TurnError([]agent.AgentMessage{assistantMsg(ai.StopReasonError, "Connection error.")})
	if err == nil {
		t.Fatal("error turn: got nil, want *TurnFailure")
	}
	var failure *agent.TurnFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error turn: %T is not *TurnFailure", err)
	}
	if failure.Message != "Connection error." {
		t.Fatalf("TurnFailure.Message = %q, want Connection error.", failure.Message)
	}

	// An error stop without a provider message still reports.
	if err := agent.TurnError([]agent.AgentMessage{assistantMsg(ai.StopReasonError, "")}); err == nil {
		t.Fatal("error turn without message: got nil, want *TurnFailure")
	}
}

func ExampleTurnError() {
	msgs := []agent.AgentMessage{assistantMsg(ai.StopReasonError, "429 Too Many Requests")}
	fmt.Println(agent.TurnError(msgs))
	// Output: turn failed: 429 Too Many Requests
}

