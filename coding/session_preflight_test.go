package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func TestPreparedPromptBuffersBashBeforeFirstEvent(t *testing.T) {
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
	p, err := s.PreparePrompt(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginPreparedPrompt(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	s.RecordBashResult("echo held", BashResult{Output: "held", ExitCode: 0}, false)
	before := len(s.Messages())
	_, err = run.Run()
	if err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("bash changed the transcript before the claimed prompt's first event: %d messages", before)
	}
	messages := s.Messages()
	if len(messages) == 0 || messages[len(messages)-1].Role() != agent.RoleBashExecution {
		t.Fatalf("bash was not retained after the run: %v", messages)
	}
}

// AgentSession.prompt can finish two preflights before either run completes. Agent.prompt rejects the second claim instead of running it after the first.
func TestPreparedPromptClaimsBeforeExecution(t *testing.T) {
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
	p, err := s.PreparePrompt(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.IsStreaming() {
		t.Fatal("preparation marked the run active")
	}
	r, err := s.BeginPreparedPrompt(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsStreaming() || !s.Agent().IsStreaming() {
		t.Fatal("run was not claimed before execution")
	}
	p2, err := s.PreparePrompt(t.Context(), BuildUserContent("second", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.BeginPreparedPrompt(t.Context(), p2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Run(); !errors.Is(err, agent.ErrAlreadyProcessingPrompt) {
		t.Fatalf("second claim=%v", err)
	}
	s.RequestAbort()
	if _, err := r.Run(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Agent().IsStreaming() {
		t.Fatal("aborted claim retained")
	}
}
