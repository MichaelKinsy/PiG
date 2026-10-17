package coding

// Ports .upstream/v0.99.1/packages/coding-agent/test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts (1 case, changed:
// `preflightResult` receives a PromptDisposition and is not called for a rejected prompt) and the dispositions that
// agent-session.ts:289-303,1890-2030,2090-2147 gives prompt, steer and followUp. The upstream suite asserts only the rejected
// case and the "queued" results of agent-session-concurrent.test.ts:164,180 (see session_concurrent_upstream_test.go); the other
// dispositions are Go regression guards for the same source lines.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream 7150-rpc-prompt-during-compaction.test.ts:16-91.
func TestPromptRejectedDuringManualCompactionReportsNoDisposition(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, defaultTools: true, settings: `{"compaction":{"keepRecentTokens":1}}`, extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeCompactEvent)
			close(started)
			<-released
			raw, err := json.Marshal(event.Preparation)
			if err != nil {
				return nil, err
			}
			var preparation struct {
				FirstKeptEntryID string
				TokensBefore     int
			}
			if err := json.Unmarshal(raw, &preparation); err != nil {
				return nil, err
			}
			return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: "manual compacted", FirstKeptEntryID: preparation.FirstKeptEntryID, TokensBefore: preparation.TokensBefore, Details: map[string]any{}}}, nil
		}},
	}}})
	inner := h.session.Inner()
	now := time.Now().UnixMilli()
	if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "old user message"}}, Timestamp: now - 1000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old assistant response"}}, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonStop, Timestamp: now - 500}}); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()
	h.provider.responses = []scriptedResponse{fauxReply("probe response", ai.StopReasonStop, 0)}

	compacted := make(chan error, 1)
	go func() { _, err := h.session.Compact(t.Context(), ""); compacted <- err }()
	<-started

	var preflight []PromptDisposition
	promptErr := h.session.Prompt(t.Context(), "PROBE-7150", &PromptOptions{Source: extension.InputSourceRPC, PreflightResult: func(result PromptDisposition) { preflight = append(preflight, result) }})
	release()
	if err := <-compacted; err != nil {
		t.Fatal(err)
	}

	if len(preflight) != 0 {
		t.Fatalf("preflightResult was called with %q for a rejected prompt", preflight)
	}
	if promptErr == nil || !strings.Contains(promptErr.Error(), "compaction is in progress") {
		t.Fatalf("prompt error = %v", promptErr)
	}
	for _, text := range queueUserTexts(h) {
		if text == "PROBE-7150" {
			t.Fatal("the rejected prompt reached the transcript")
		}
	}
	for _, entry := range h.entries("message") {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.User != nil && extractUserMessageText(message.Message.User.Content) == "PROBE-7150" {
			t.Fatal("the rejected prompt was persisted")
		}
	}
	h.mu.Lock()
	events := append([]agent.AgentEvent(nil), h.events...)
	h.mu.Unlock()
	for _, event := range events {
		switch event.(type) {
		case agent.AgentStartEvent, agent.AgentSettledEvent:
			t.Fatalf("a rejected prompt produced %T", event)
		}
	}
}

// agent-session.ts:1959 (`preflightResult?.("started")`) and :2010: a prompt that starts a turn reports "started", once.
func TestPromptReportsStartedForATurn(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("hello", ai.StopReasonStop, 0))
	var got []PromptDisposition
	if err := h.session.Prompt(t.Context(), "hi", &PromptOptions{PreflightResult: func(result PromptDisposition) { got = append(got, result) }}); err != nil {
		t.Fatal(err)
	}
	if want := []PromptDisposition{DispositionStarted}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dispositions = %q, want %q", got, want)
	}
}

// agent-session.ts:1893-1898,1911-1914: an extension command or an input handler that consumes the input reports "handled".
func TestPromptReportsHandledForCommandsAndInputHandlers(t *testing.T) {
	ran := false
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{
		Commands: map[string]extension.RegisteredCommand{"probe": {Name: "probe", Handler: func(_ context.Context, _ string) error { ran = true; return nil }}},
		Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			if strings.HasPrefix(args[0].(extension.InputEvent).Text, "swallow") {
				return extension.InputEventResultHandled{}, nil
			}
			return extension.InputEventResultContinue{}, nil
		}}},
	}}, fauxReply("unused", ai.StopReasonStop, 0))
	for _, text := range []string{"/probe now", "swallow this"} {
		var got []PromptDisposition
		if err := h.session.Prompt(t.Context(), text, &PromptOptions{PreflightResult: func(result PromptDisposition) { got = append(got, result) }}); err != nil {
			t.Fatal(err)
		}
		if want := []PromptDisposition{DispositionHandled}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: dispositions = %q, want %q", text, got, want)
		}
	}
	if !ran {
		t.Fatal("the extension command did not run")
	}
	if h.provider.callCount() != 0 {
		t.Fatalf("a handled input called the model %d times", h.provider.callCount())
	}
}

// agent-session.ts:1925-1938: a prompt queued behind a running turn reports "queued"; steer and followUp report "handled" when an input handler consumes the input (agent-session.ts:2104) and "queued" otherwise (:2114).
func TestQueuedInputDispositions(t *testing.T) {
	waiting := createQueueWaitingHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
		if strings.HasPrefix(args[0].(extension.InputEvent).Text, "handle") {
			return extension.InputEventResultHandled{}, nil
		}
		return extension.InputEventResultContinue{}, nil
	}}}})
	h := waiting.h
	waiting.setResponses(fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0), fauxReply("more", ai.StopReasonStop, 0))
	<-waiting.waitForToolStart

	var got []PromptDisposition
	if err := h.session.Prompt(t.Context(), "later", &PromptOptions{StreamingBehavior: extension.DeliverAsFollowUp, PreflightResult: func(result PromptDisposition) { got = append(got, result) }}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name string
		run  func(string) (QueuedInputDisposition, error)
	}{
		{"steer", func(text string) (QueuedInputDisposition, error) { return h.session.Steer(t.Context(), text, nil, nil) }},
		{"followUp", func(text string) (QueuedInputDisposition, error) {
			return h.session.FollowUp(t.Context(), text, nil, nil)
		}},
	} {
		queued, err := step.run("queue me")
		if err != nil || queued != DispositionQueued {
			t.Fatalf("%s = %q, %v; want %q", step.name, queued, err, DispositionQueued)
		}
		handled, err := step.run("handle me")
		if err != nil || handled != DispositionHandled {
			t.Fatalf("%s = %q, %v; want %q", step.name, handled, err, DispositionHandled)
		}
	}
	if want := []PromptDisposition{DispositionQueued}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt dispositions = %q, want %q", got, want)
	}
	waiting.releaseToolExecution()
	waiting.join()
}
