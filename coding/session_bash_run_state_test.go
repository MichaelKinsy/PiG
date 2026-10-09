// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi agent-session.ts:3509 buffers shell records while the Session run is active,
// independently of whether a state lock is currently held by the provider loop.
func TestBashRecordingDefersDuringActualUnlockedAgentRun(t *testing.T) {
	provider := &blockingProviderForSessionTests{started: make(chan struct{})}
	s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	go func() { _, err := s.Send(t.Context(), "wait for abort"); done <- err }()
	<-provider.started
	if err := s.RecordBashResult("echo buffered", BashResult{Output: "buffered", ExitCode: new(0)}, false); err != nil {
		t.Fatal(err)
	}
	for _, entry := range s.inner.GetEntries() {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Role() == agent.RoleBashExecution {
			t.Error("shell result persisted while the agent run was active")
		}
	}
	s.RequestAbort()
	<-done
	entries := s.inner.GetEntries()
	if len(entries) == 0 {
		t.Fatal("shell result was not flushed after the run")
	}
	last, ok := entries[len(entries)-1].(icodingagent.MessageEntry)
	if !ok || last.Message.Role() != agent.RoleBashExecution {
		t.Fatal("shell result was not flushed as a bashExecution message after the run")
	}
	messages := s.Messages()
	if len(messages) == 0 || messages[len(messages)-1].Role() != agent.RoleBashExecution {
		t.Fatal("shell result was not restored to model context")
	}
	fmt.Printf("BASH_ORDER tail=%s\n", messages[len(messages)-1].Role())
}
