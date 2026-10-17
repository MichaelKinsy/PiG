// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"sync/atomic"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-session.ts:917-921 delivers public listeners before persistence, and
// agent.prompt awaits that handler before continuing into tool execution.
func TestSessionPublicListenersPrecedePersistenceAndTools(t *testing.T) {
	tool := &admissionTool{name: "effect", parameter: "value"}
	provider := &scriptedProvider{responses: []scriptedResponse{admissionResponse("effect", "value", "blocked"), fauxReply("unexpected continuation", ai.StopReasonStop, 0)}}
	s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true, Tools: []agent.AgentTool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	var observed atomic.Int32
	var alreadyPersisted atomic.Bool
	s.Subscribe(func(event agent.AgentEvent) {
		end, ok := event.(agent.MessageEndEvent)
		if !ok || end.Message.Assistant == nil || len(end.Message.Assistant.Content) == 0 {
			return
		}
		if _, ok := end.Message.Assistant.Content[0].(ai.ToolCall); !ok {
			return
		}
		// A listener may inspect protected Session state without deadlocking the producer.
		s.mu.Lock()
		entries := s.inner.GetEntries()
		s.mu.Unlock()
		observed.Add(1)
		for _, entry := range entries {
			if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Assistant != nil {
				alreadyPersisted.Store(true)
			}
		}
		s.RequestAbort()
	})
	if _, err := s.Send(t.Context(), "attempt the effect"); err != nil {
		t.Fatal(err)
	}
	if observed.Load() != 1 || alreadyPersisted.Load() {
		t.Fatalf("callbacks=%d alreadyPersisted=%v", observed.Load(), alreadyPersisted.Load())
	}
	if calls := tool.executions(); len(calls) != 0 {
		t.Fatalf("tool ran before subscriber abort: %v", calls)
	}
	for _, event := range drainEvents(t, s) {
		if event == nil {
			t.Fatal("internal envelope leaked to event stream")
		}
	}
	fmt.Printf("LISTENER_ORDER persisted=%t effects=%d callbacks=%d\n", alreadyPersisted.Load(), len(tool.executions()), observed.Load())
}

func BenchmarkSessionSendWithSubscriber(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		provider := &scriptedProvider{responses: []scriptedResponse{fauxReply("done", ai.StopReasonStop, 0)}}
		model := fakeModelWithProvider(provider)
		model.Capabilities.ContextWindow = 200000
		s, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "Test"})
		if err != nil {
			b.Fatal(err)
		}
		s.Subscribe(func(agent.AgentEvent) {})
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for event := range s.Events() {
				AcknowledgeEvent(event)
			}
		}()
		_, sendErr := s.Send(b.Context(), "hello")
		closeErr := s.Close()
		<-drained
		if sendErr != nil || closeErr != nil {
			b.Fatalf("send=%v close=%v", sendErr, closeErr)
		}
	}
}
