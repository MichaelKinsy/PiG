package codingagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// These tests run the interactive owner loop against a real coding.Session,
// the production pairing, with a scripted faux provider.

type scriptedReply func() *ai.AssistantMessage

type scriptedProvider struct {
	mu       sync.Mutex
	replies  []scriptedReply
	requests []string
}

func (p *scriptedProvider) ID() string   { return "faux" }
func (p *scriptedProvider) Close() error { return nil }

func (p *scriptedProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	raw, _ := json.Marshal(request.Messages())
	p.mu.Lock()
	p.requests = append(p.requests, string(raw))
	index := len(p.requests) - 1
	p.mu.Unlock()
	message := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: "no scripted reply", Timestamp: time.Now().UnixMilli()}
	if index < len(p.replies) {
		message = p.replies[index]()
	}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	if message.StopReason == ai.StopReasonError {
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: message})
	} else {
		_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	}
	return stream, nil
}

func (p *scriptedProvider) requestLog() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

func reply(text string, usage ai.Usage) scriptedReply {
	return func() *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.TextContent{Text: text}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Usage: usage,
			Timestamp: time.Now().UnixMilli(),
		}
	}
}

func replyError(message string) scriptedReply {
	return func() *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: message, Timestamp: time.Now().UnixMilli()}
	}
}

type sessionPair struct {
	session  *coding.Session
	provider *scriptedProvider
	harness  *icodingagent.TestHarness
}

func newSessionPair(t *testing.T, settings string, contextWindow int, onEvent func(*icodingagent.TestHarness, agent.AgentEvent), replies ...scriptedReply) *sessionPair {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{replies: replies}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: contextWindow}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	harness := icodingagent.NewTestHarness(t, icodingagent.InteractiveOptions{
		CWD: cwd, AgentDir: agentDir, Model: model,
		SessionHandle: session, SettingsManager: services.SettingsManager(),
	}, onEvent)
	t.Cleanup(func() { _ = session.Close() })
	return &sessionPair{session: session, provider: provider, harness: harness}
}

// A message typed while the end-of-turn auto-compaction runs is delivered to
// the model in the same run. Upstream's _runAgentPrompt loop continues while
// agent.hasQueuedMessages() after post-run handling and before settling; the
// interactive mode used to end the run with the message stranded in the
// steering queue and the session idle.
func TestInteractiveMessageQueuedDuringEndOfTurnCompactionIsDelivered(t *testing.T) {
	typed := false
	// The first answer's usage passes the threshold (window minus the default
	// 16384 reserve), so the run ends with a threshold compaction.
	pair := newSessionPair(t, `{"compaction":{"keepRecentTokens":1},"retry":{"enabled":false}}`, 100000,
		func(h *icodingagent.TestHarness, ev agent.AgentEvent) {
			if _, ok := ev.(agent.CompactionStartEvent); ok && !typed {
				typed = true
				h.Enter("typed during compaction")
			}
		},
		reply("first answer", ai.Usage{Input: 90000, Output: 10}),
		reply("compaction summary", ai.Usage{}),
		reply("second answer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter(strings.Repeat("x", 5000)) })

	deadline := time.Now().Add(10 * time.Second)
	for len(pair.provider.requestLog()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pair.harness.WaitIdle(t, 10*time.Second)

	requests := pair.provider.requestLog()
	if len(requests) != 3 {
		steering, followUp := pair.harness.QueuedMessages()
		t.Fatalf("model requests = %d, want 3 (the queued message was stranded: steering=%d followUp=%d)", len(requests), steering, followUp)
	}
	if !strings.Contains(requests[2], "typed during compaction") {
		t.Fatalf("third request does not carry the queued message:\n%s", requests[2])
	}
	if steering, followUp := pair.harness.QueuedMessages(); steering != 0 || followUp != 0 {
		t.Fatalf("queues after settle: steering=%d followUp=%d, want empty", steering, followUp)
	}
}

// A retry that ends in a context overflow still gets overflow recovery, and
// the failed attempts are omitted durably. Upstream's post-run loop re-enters
// _handlePostAgentRun after every continuation (retry, then _checkCompaction)
// and omits a retried attempt with a context_edit; the interactive mode's own
// retry loop reported the overflow as a successful retry and stopped.
func TestInteractiveRetryThenOverflowCompactsAndRetries(t *testing.T) {
	pair := newSessionPair(t, `{"compaction":{"keepRecentTokens":1},"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1,"maxDelayMs":1}}`, 100000, nil,
		replyError("overloaded_error"),
		replyError("prompt is too long: 213462 tokens > 200000 maximum"),
		reply("compaction summary", ai.Usage{}),
		reply("recovered answer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter(strings.Repeat("x", 5000)) })

	deadline := time.Now().Add(10 * time.Second)
	for len(pair.provider.requestLog()) < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pair.harness.WaitIdle(t, 10*time.Second)

	if got := len(pair.provider.requestLog()); got != 4 {
		t.Fatalf("model requests = %d, want 4 (retry, overflow compaction, recovered retry)", got)
	}
	entries := pair.session.Inner().Entries()
	var compactions, omissions int
	for _, entry := range entries {
		switch entry.Base.Type {
		case "compaction":
			compactions++
		case "context_edit":
			omissions++
		}
	}
	if compactions != 1 {
		t.Fatalf("compaction entries = %d, want 1", compactions)
	}
	if omissions < 2 {
		t.Fatalf("context_edit entries = %d, want the retried and the overflowed attempts omitted", omissions)
	}
	if last := pair.session.LastAssistantText(); last == nil || *last != "recovered answer" {
		t.Fatalf("last assistant text = %v, want the recovered answer", last)
	}
}

// Esc during an automatic-retry countdown cancels only the delay. Upstream
// swaps the editor's Escape handler for session.abortRetry() while the retry
// status shows, so the run settles normally and input queued meanwhile is
// still delivered; interactive mode aborted the whole run and stranded it.
func TestInteractiveEscDuringRetryCountdownCancelsOnlyTheRetry(t *testing.T) {
	queued := false
	pair := newSessionPair(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":30000,"maxDelayMs":30000}}`, 100000,
		func(h *icodingagent.TestHarness, ev agent.AgentEvent) {
			if _, ok := ev.(agent.AutoRetryStartEvent); ok && !queued {
				queued = true
				h.Enter("steer during the countdown")
				// The user presses Esc while the countdown shows, after the
				// Session has armed the retry delay.
				go func() {
					time.Sleep(200 * time.Millisecond)
					h.Do(func() { h.Key("\x1b") })
				}()
			}
		},
		replyError("overloaded_error"),
		reply("answer to the steer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter("start") })

	deadline := time.Now().Add(10 * time.Second)
	for len(pair.provider.requestLog()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pair.harness.WaitIdle(t, 10*time.Second)

	requests := pair.provider.requestLog()
	if len(requests) != 2 || !strings.Contains(requests[1], "steer during the countdown") {
		t.Fatalf("model requests = %d, want the queued steer delivered after the cancelled retry", len(requests))
	}
	if !strings.Contains(pair.harness.Chat(), "Retry failed after 1 attempts: Retry cancelled") {
		t.Fatalf("chat lacks the cancelled-retry error:\n%s", pair.harness.Chat())
	}
}

// Esc while a run streams puts queued messages back in the editor before it
// aborts (upstream onEscape → restoreQueuedMessagesToEditor({ abort: true })),
// so they are neither lost nor injected into the next, unrelated prompt.
func TestInteractiveEscRestoresQueuedMessagesToTheEditor(t *testing.T) {
	requested := make(chan struct{})
	release := make(chan struct{})
	pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil,
		func() *ai.AssistantMessage {
			// The first request is in flight, so the run is streaming.
			close(requested)
			<-release
			return reply("first answer", ai.Usage{Input: 10, Output: 10})()
		},
		reply("next answer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter("start") })
	<-requested
	pair.harness.Do(func() {
		pair.harness.Enter("also update the docs")
		pair.harness.Key("\x1b")
	})
	close(release)
	pair.harness.WaitIdle(t, 10*time.Second)

	if got := pair.harness.EditorText(); got != "also update the docs" {
		t.Fatalf("editor after Esc = %q, want the queued message restored", got)
	}
	if steering, followUp := pair.harness.QueuedMessages(); steering != 0 || followUp != 0 {
		t.Fatalf("queues after Esc: steering=%d followUp=%d, want empty", steering, followUp)
	}
	pair.harness.Do(func() { pair.harness.Enter("revert that") })
	pair.harness.WaitIdle(t, 10*time.Second)
	for _, request := range pair.provider.requestLog() {
		if strings.Contains(request, "also update the docs") {
			t.Fatalf("the restored message leaked into a model request:\n%s", request)
		}
	}
}

// `pig "first" "second" "third"` sends every positional message as its own
// prompt, each after the previous run settles (upstream initialMessages).
// Interactive mode sent only the first (MODES-05).
func TestInteractiveSendsEveryInitialMessageInOrder(t *testing.T) {
	pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil,
		reply("one", ai.Usage{Input: 10, Output: 10}),
		reply("two", ai.Usage{Input: 10, Output: 10}),
		reply("three", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter("first") })
	pair.harness.SubmitInitialMessages([]string{"second", "third"})
	pair.harness.WaitIdle(t, 10*time.Second)

	requests := pair.provider.requestLog()
	if len(requests) != 3 {
		t.Fatalf("model requests = %d, want one per positional message", len(requests))
	}
	if !strings.Contains(requests[1], `"second"`) || strings.Contains(requests[1], `"third"`) {
		t.Fatalf("second request is not the second message on its own:\n%s", requests[1])
	}
	if !strings.Contains(requests[2], `"third"`) {
		t.Fatalf("third request lacks the third message:\n%s", requests[2])
	}
}
