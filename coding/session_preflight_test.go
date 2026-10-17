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
	start, err := s.BeginRunStart()
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreparePrompt(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginPreparedPrompt(t.Context(), start, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBashResult("echo held", BashResult{Output: "held", ExitCode: new(0)}, nil); err != nil {
		t.Fatal(err)
	}
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

// agent-session.ts:1832-1837 (_startRun) marks the Session busy before _prepareRun, so a second prompt is rejected by prompt()'s streaming
// check (:2098-2103) while the first one prepares, and the first claims the Agent before execution.
func TestPromptRunStartClaimsTheSessionBeforePreparation(t *testing.T) {
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
	start, err := s.BeginRunStart()
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsStreaming() || s.IsIdle() {
		t.Fatal("the run start did not mark the Session busy")
	}
	if _, err := s.BeginRunStart(); err == nil || err.Error() != errAgentAlreadyProcessing.Error() {
		t.Fatalf("a second start while the first prepares = %v, want %q", err, errAgentAlreadyProcessing)
	}
	p, err := s.PreparePrompt(t.Context(), BuildUserContent("first", nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent().IsStreaming() {
		t.Fatal("preparation claimed the Agent")
	}
	r, err := s.BeginPreparedPrompt(t.Context(), start, p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsStreaming() || !s.Agent().IsStreaming() {
		t.Fatal("run was not claimed before execution")
	}
	s.RequestAbort()
	if _, err := r.Run(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Agent().IsStreaming() || !s.IsIdle() {
		t.Fatal("aborted claim retained")
	}
}

// An abandoned start returns the Session to idle (agent-session.ts:1838-1843, _startRun's catch).
func TestAbandonedRunStartReturnsTheSessionToIdle(t *testing.T) {
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
	start, err := s.BeginRunStart()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBashResult("echo held", BashResult{Output: "held", ExitCode: new(0)}, nil); err != nil {
		t.Fatal(err)
	}
	start.Abandon()
	if !s.IsIdle() {
		t.Fatal("the abandoned start left the Session busy")
	}
	if messages := s.Messages(); len(messages) != 1 || messages[0].Role() != agent.RoleBashExecution {
		t.Fatalf("the held bash result was not appended: %v", messages)
	}
	if err := s.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
}
