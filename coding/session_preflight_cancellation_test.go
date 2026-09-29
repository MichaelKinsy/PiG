package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi agent-session.ts:1468-1488 and agent.ts:499-525 keep the live Agent's abort controller when another prepared prompt loses admission.
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
	prepared, err := s.PreparePrompt(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginPreparedPrompt(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	defer func() {
		if !ran {
			_, _ = run.Run()
		}
	}()
	signal := s.Agent().Signal()
	second, err := s.PreparePrompt(t.Context(), BuildUserContent("second", nil))
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := s.BeginPreparedPrompt(t.Context(), second)
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
	_, err = run.Run()
	ran = true
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Agent().IsStreaming() || s.Agent().Signal() != nil {
		t.Fatal("aborted claim retained active state")
	}
}
