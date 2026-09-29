// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type unrelatedAuthAbortError struct{ Name string }

func (unrelatedAuthAbortError) Error() string { return "auth failed" }
func seedCancellationCompaction(t *testing.T, s *Session) {
	t.Helper()
	persistQueueMessage(t, s, queueUser(strings.Repeat("x", 500), 1))
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, strings.Repeat("y", 200), 100, 0, ai.StopReasonStop, 2)})
	s.RefreshContext()
}
func lastCompactionEnd(t *testing.T, s *Session) *agent.CompactionEndEvent {
	t.Helper()
	var last *agent.CompactionEndEvent
	for _, event := range drainEvents(t, s) {
		if end, ok := event.(agent.CompactionEndEvent); ok {
			last = &end
		}
	}
	return last
}
func installCompactionModel(s *Session, provider ai.Provider, window, maxTokens int) {
	model := fakeModelWithProvider(provider)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = window
	model.Capabilities.MaxOutputTokens = maxTokens
	s.agent.SetModel(model)
}

func TestAutomaticCompactionCancellationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts:47
	t.Run("does not start post-run auto-compaction after abort", func(t *testing.T) {
		s := newTreeTestSessionWithSettings(t, `{"compaction":{"enabled":true,"reserveTokens":50,"keepRecentTokens":1},"retry":{"enabled":false}}`)
		p := &scriptedProvider{responses: []scriptedResponse{fauxError("Synthetic network failure")}}
		installCompactionModel(s, p, 200, 50)
		withTreeHandlers(s, t, map[string][]extension.HandlerFn{"session_before_compact": {func(...any) (any, error) { return extension.SessionBeforeCompactResult{Cancel: true}, nil }}})
		seedCancellationCompaction(t, s)
		s.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil {
				// A synchronous subscriber remains part of the operation even when the Go scheduler preempts it.
				runtime.Gosched()
				s.AbortCompaction()
				s.RequestAbort()
			}
		})
		if _, err := s.Send(t.Context(), strings.Repeat("z", 1000)); err != nil {
			t.Fatal(err)
		}
		starts := 0
		for _, event := range drainEvents(t, s) {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				starts++
			}
		}
		if starts != 0 {
			t.Fatalf("post-abort compactions=%d", starts)
		}
		fmt.Printf("POST_ABORT compactions=%d\n", starts)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts:78
	t.Run("cancels summarization authentication", func(t *testing.T) {
		s := autoQueueSession(t, true)
		authStarted := make(chan context.Context, 1)
		provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "faux", Model: "faux-1", BaseURL: "http://unused.invalid", GetAPIKey: func(ctx context.Context) (string, error) {
			authStarted <- ctx
			if ctx.Done() == nil {
				return "", errors.New("Missing auth abort signal")
			}
			<-ctx.Done()
			return "", ctx.Err()
		}})
		installCompactionModel(s, provider, 200000, 8192)
		seedCancellationCompaction(t, s)
		var starts atomic.Int32
		s.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				starts.Add(1)
			}
		})
		done := make(chan bool, 1)
		go func() {
			compacted, err := s.runAutoCompaction(t.Context(), "threshold", false)
			if err != nil {
				t.Error(err)
			}
			done <- compacted
		}()
		var authContext context.Context
		select {
		case authContext = <-authStarted:
		case result := <-done:
			t.Fatalf("compaction ended before auth: %v", result)
		}
		started, wasCompacting := starts.Load(), s.IsCompacting()
		if err := s.Abort(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-done
		if started != 1 || !wasCompacting || !errors.Is(authContext.Err(), context.Canceled) {
			t.Fatalf("started=%d compacting=%v authErr=%v", started, wasCompacting, authContext.Err())
		}
		if end := lastCompactionEnd(t, s); end == nil || !end.Aborted {
			t.Fatalf("end=%+v", end)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts:111
	t.Run("cancels synchronously from compaction_start", func(t *testing.T) {
		s := autoQueueSession(t, true)
		p := &scriptedProvider{}
		installCompactionModel(s, p, 200000, 8192)
		seedCancellationCompaction(t, s)
		s.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				s.AbortCompaction()
			}
		})
		if _, err := s.runAutoCompaction(t.Context(), "threshold", false); err != nil {
			t.Fatal(err)
		}
		if p.callCount() != 0 {
			t.Fatal("provider called after synchronous cancellation")
		}
		if end := lastCompactionEnd(t, s); end == nil || !end.Aborted {
			t.Fatalf("end=%+v", end)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts:126 (both table rows)
	for _, tc := range []struct {
		name string
		err  error
	}{{"matching error text", errors.New("Compaction cancelled")}, {"an unrelated AbortError", unrelatedAuthAbortError{Name: "AbortError"}}} {
		t.Run("reports "+tc.name+" as a failure", func(t *testing.T) {
			s := autoQueueSession(t, true)
			provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "faux", Model: "faux-1", BaseURL: "http://unused.invalid", GetAPIKey: func(context.Context) (string, error) { return "", tc.err }})
			installCompactionModel(s, provider, 200000, 8192)
			seedCancellationCompaction(t, s)
			if _, err := s.runAutoCompaction(t.Context(), "threshold", false); err != nil {
				t.Fatal(err)
			}
			end := lastCompactionEnd(t, s)
			if end == nil || end.Aborted || !strings.Contains(end.ErrorMessage, tc.err.Error()) {
				t.Fatalf("end=%+v", end)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts:142
	t.Run("reports extension cancellation as aborted", func(t *testing.T) {
		s := autoQueueSession(t, true)
		seedCancellationCompaction(t, s)
		withTreeHandlers(s, t, map[string][]extension.HandlerFn{"session_before_compact": {func(...any) (any, error) { return extension.SessionBeforeCompactResult{Cancel: true}, nil }}})
		if _, err := s.runAutoCompaction(t.Context(), "threshold", false); err != nil {
			t.Fatal(err)
		}
		if end := lastCompactionEnd(t, s); end == nil || !end.Aborted {
			t.Fatalf("end=%+v", end)
		}
	})
}
