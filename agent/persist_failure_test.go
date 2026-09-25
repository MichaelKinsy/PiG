package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A failing message_end persistence stops the loop and ends the run with an
// error assistant message, turn_end, and agent_end, like upstream
// Agent.handleRunFailure after a throwing listener.
func TestPersistFailureEndsRunWithErrorMessage(t *testing.T) {
	writeErr := errors.New("ENOSPC: no space left on device")
	var executed int
	tool := valueEchoTool(ToolModeParallel, func(string) { executed++ })
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("call-1", tool.name, ai.JsonObject{"value": "x"}))}
	rec := newEventRecorder(nil)
	failed := false
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider), Tools: []AgentTool{tool}, EventCh: rec.ch,
		OnMessagePersist: func(m AgentMessage) error {
			if m.Assistant != nil && !failed {
				failed = true
				return writeErr
			}
			return nil
		},
	})
	messages, err := a.Send(context.Background(), "go")
	events := rec.stop()
	if err != nil {
		t.Fatalf("Send error %v; the failure message persisted, so the run ends normally", err)
	}
	last := messages[len(messages)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != writeErr.Error() {
		t.Fatalf("last message %+v, want the persistence error", messages[len(messages)-1])
	}
	if executed != 0 || provider.calls() != 1 {
		t.Fatalf("tool executions %d, provider calls %d; the run must stop at the failure", executed, provider.calls())
	}
	agentEnds := 0
	for _, ev := range events {
		if _, ok := ev.(AgentEndEvent); ok {
			agentEnds++
		}
	}
	if agentEnds != 1 {
		t.Fatalf("agent_end events = %d, want 1", agentEnds)
	}
}

// When the failure message cannot be persisted either, the run fails with
// the error, as upstream's handleRunFailure rethrows.
func TestPersistFailureOfFailureMessageReturnsError(t *testing.T) {
	writeErr := errors.New("EACCES: permission denied")
	provider := &scriptedProvider{respond: replyText("ok")}
	a := NewAgent(AgentOptions{
		Model:            scriptedModel(provider),
		OnMessagePersist: func(AgentMessage) error { return writeErr },
	})
	if _, err := a.Send(context.Background(), "go"); !errors.Is(err, writeErr) {
		t.Fatalf("Send error %v, want %v", err, writeErr)
	}
	if a.IsStreaming() {
		t.Fatal("agent still streaming after a failed run")
	}
}
