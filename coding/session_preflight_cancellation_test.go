package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// agent-session.ts:1857-1876 and agent.ts:341-345: when agent.prompt rejects a start because another run holds the Agent, _startRun's
// finally settles the start and the error propagates, and the live run keeps its abort controller.
func TestPreparedPromptAbortReachesClaimedSignal(t *testing.T) {
	s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range s.Events() {
		}
	}()
	defer func() { _ = s.Close(); <-done }()
	held, err := s.Agent().BeginSendContent(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	defer func() {
		if !ran {
			_, _ = held.Run()
		}
	}()
	signal := s.Agent().Signal()
	start, err := s.BeginRunStart()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.PreparePrompt(t.Context(), BuildUserContent("second", nil))
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := s.BeginPreparedPrompt(t.Context(), start, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejected.Run(); !errors.Is(err, agent.ErrAlreadyProcessingPrompt) {
		t.Errorf("second admission=%v", err)
	}
	s.RequestAbort()
	if signal == nil || !errors.Is(signal.Err(), context.Canceled) {
		t.Errorf("Session abort did not reach the claimed Agent signal: %v", signal)
	}
	_, err = held.Run()
	ran = true
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Agent().IsStreaming() || s.Agent().Signal() != nil {
		t.Fatal("aborted claim retained active state")
	}
}
