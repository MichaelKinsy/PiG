package coding

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
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports of the packages/coding-agent/test/suite/agent-session-boundaries.test.ts
// cases that exercise Session recovery and context accounting without the
// actionable turn_end / agent_before_settle extension boundaries.

// scriptedResponse builds one faux provider response from the request.
type scriptedResponse func(request []ai.Message) *ai.AssistantMessage

// scriptedProvider answers each model call with the next scripted response,
// like the upstream faux harness. It records every request.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []scriptedResponse
	requests  []string
}

func (p *scriptedProvider) ID() string   { return "faux" }
func (p *scriptedProvider) Close() error { return nil }

func (p *scriptedProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	messages := request.Messages()
	raw, _ := json.Marshal(messages)
	p.requests = append(p.requests, string(raw))
	index := len(p.requests) - 1
	p.mu.Unlock()
	message := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, ErrorMessage: "no scripted response", Timestamp: time.Now().UnixMilli()}
	if index < len(p.responses) {
		message = p.responses[index](messages)
	}
	if message.StopReason == ai.StopReasonError {
		return newSessionTestStream(ai.StartEvent{Partial: message}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: message}), nil
	}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: message.StopReason, Message: message}), nil
}

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func fauxReply(text string, reason ai.StopReason, offset time.Duration) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		var content []ai.AssistantContentBlock
		if text != "" {
			content = append(content, ai.TextContent{Text: text})
		}
		return &ai.AssistantMessage{Content: content, Provider: "faux", Model: "faux-1", StopReason: reason, Timestamp: time.Now().Add(offset).UnixMilli()}
	}
}

func fauxError(message string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: message, Timestamp: time.Now().UnixMilli()}
	}
}

func fauxToolCall(name string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.ToolCall{ID: "call-" + name, Name: name, Arguments: ai.JsonObject{}}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
		}
	}
}

type harnessOptions struct {
	settings      string
	contextWindow int
	maxTokens     int
	tools         []agent.AgentTool
	extension     extension.Extension
}

type recoveryHarness struct {
	session  *Session
	provider *scriptedProvider
	mu       sync.Mutex
	events   []agent.AgentEvent
	done     chan struct{}
}

func newRecoveryHarness(t *testing.T, opts harnessOptions, responses ...scriptedResponse) *recoveryHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := opts.settings
	if settings == "" {
		settings = "{}"
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{responses: responses}
	contextWindow := opts.contextWindow
	if contextWindow == 0 {
		contextWindow = 128_000
	}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: contextWindow, MaxOutputTokens: opts.maxTokens}}
	var runner *inproc.Runner
	if opts.extension.Handlers != nil {
		runner = inproc.NewRunner([]extension.Extension{opts.extension}, t.TempDir())
	}
	session, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true, Tools: opts.tools, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	h := &recoveryHarness{session: session, provider: provider, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		for event := range session.Events() {
			h.mu.Lock()
			h.events = append(h.events, event)
			h.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = session.Close()
		<-h.done
	})
	return h
}

// settle waits until the Session's ordered agent_settled event is published.
func (h *recoveryHarness) settle(t *testing.T) []agent.AgentEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		for _, event := range h.events {
			if _, ok := event.(agent.AgentSettledEvent); ok {
				events := append([]agent.AgentEvent(nil), h.events...)
				h.mu.Unlock()
				return events
			}
		}
		h.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("agent_settled was not published")
	return nil
}

func (h *recoveryHarness) entries(entryType string) []icodingagent.SessionEntry {
	var out []icodingagent.SessionEntry
	for _, entry := range h.session.Inner().Entries() {
		if entry.Base.Type == entryType {
			out = append(out, entry)
		}
	}
	return out
}

// omittedTargets returns each context_edit target with whether its latest
// edit omits it.
func (h *recoveryHarness) omittedTargets(t *testing.T) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	for _, entry := range h.entries("context_edit") {
		var edit icodingagent.ContextEditEntry
		if err := json.Unmarshal(entry.Raw(), &edit); err != nil {
			t.Fatal(err)
		}
		out[edit.TargetID] = edit.Replacement == nil
	}
	return out
}

func messageEntryIDs(t *testing.T, h *recoveryHarness, match func(*agent.AssistantMessage) bool) []string {
	t.Helper()
	var ids []string
	for _, entry := range h.entries("message") {
		message, ok := entry.AsMessage()
		if ok && message.Message.Assistant != nil && match(message.Message.Assistant) {
			ids = append(ids, entry.Base.ID)
		}
	}
	return ids
}

func projectionContains(h *recoveryHarness, text string) bool {
	raw, _ := json.Marshal(h.session.Inner().BuildSessionProjection().Messages)
	return strings.Contains(string(raw), text)
}

func summaryFromPreparation(summary string) extension.Extension {
	return extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeCompactEvent)
			raw, err := json.Marshal(event.Preparation)
			if err != nil {
				return nil, err
			}
			var prep struct {
				FirstKeptEntryID string
				TokensBefore     int
			}
			if err := json.Unmarshal(raw, &prep); err != nil {
				return nil, err
			}
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{
				"summary": summary, "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore,
			}}, nil
		}},
	}}
}

func TestBoundaryDoesNotTriggerThresholdCompactionFromPostEditUsageCapturedBeforeALaterCompaction(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`, contextWindow: 10_000, maxTokens: 100})
	inner := h.session.Inner()
	userID, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "small input"}}, Timestamp: time.Now().UnixMilli() - 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendContextEdit(userID, &icodingagent.ContextEditReplacement{Content: json.RawMessage(`"edited input"`)}); err != nil {
		t.Fatal(err)
	}
	response := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, Provider: "faux", ModelID: "faux-1",
		Usage: &ai.Usage{Input: 50_000, Output: 1, TotalTokens: 50_001}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli() - 2}
	if _, err := inner.AppendMessage(agent.AgentMessage{Assistant: response}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendCompaction("small summary", userID, 50_001, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()
	errorMessage := &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: "invalid_api_key", Timestamp: time.Now().UnixMilli() + 1_000}

	if continueRun, err := h.session.checkCompaction(context.Background(), errorMessage, true, nil); err != nil || continueRun {
		t.Fatalf("checkCompaction = %v, %v", continueRun, err)
	}
	if n := len(h.entries("compaction")); n != 1 {
		t.Fatalf("compactions = %d, want the original 1", n)
	}
}

func TestBoundaryDoesNotTreatRetainedPreCompactionAssistantUsageAsPostCompactionUsage(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	inner := h.session.Inner()
	retained := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "retained"}}, Provider: "faux", ModelID: "faux-1",
		Usage: &ai.Usage{Input: 10_000, TotalTokens: 10_001}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	retainedID, err := inner.AppendMessage(agent.AgentMessage{Assistant: retained})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendCompaction("summary", retainedID, 10_001, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()

	usage := h.session.ContextUsage()
	if usage == nil || usage.Tokens != nil || usage.Percent != nil {
		t.Fatalf("context usage = %+v, want unknown tokens", usage)
	}
}

func TestDurableLengthRecoveryResetsAfterASuccessfulIntermediateAssistantTurn(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{
		settings:      `{"compaction":{"keepRecentTokens":1,"reserveTokens":0}}`,
		contextWindow: 1000, maxTokens: 100,
		tools:     []agent.AgentTool{&fakeTool{name: "noop"}},
		extension: summaryFromPreparation("recovered input"),
	},
		fauxReply("first partial", ai.StopReasonLength, 0),
		fauxToolCall("noop"),
		fauxReply("second partial", ai.StopReasonLength, time.Second),
		fauxReply("completed second recovery", ai.StopReasonStop, 2*time.Second),
	)

	if _, err := h.session.Send(context.Background(), strings.Repeat("x", 5000)); err != nil {
		t.Fatal(err)
	}
	h.settle(t)

	lengthIDs := messageEntryIDs(t, h, func(m *agent.AssistantMessage) bool { return m.StopReason == ai.StopReasonLength })
	if len(lengthIDs) != 2 {
		t.Fatalf("length responses = %d, want 2", len(lengthIDs))
	}
	omitted := h.omittedTargets(t)
	for _, id := range lengthIDs {
		if !omitted[id] {
			t.Fatalf("length response %s was not omitted; edits = %v", id, omitted)
		}
	}
	if got := h.provider.callCount(); got != 3 {
		t.Fatalf("model calls = %d, want 3", got)
	}
}

func TestDurableRecoveryFinishesRetryBookkeepingWhenARetryReceivesANonretryableError(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`},
		fauxError("overloaded_error"),
		fauxError("invalid_api_key"),
	)

	_, _ = h.session.Send(context.Background(), "start")
	events := h.settle(t)

	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
	found := false
	for _, event := range events {
		if end, ok := event.(agent.AutoRetryEndEvent); ok && !end.Success && end.Attempt == 1 && end.FinalError == "invalid_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("auto_retry_end{success:false, attempt:1, finalError:invalid_api_key} missing from %#v", events)
	}
}

func TestDurableRecoveryOmitsARecoverableProjectedReplacementByItsSourceEntryID(t *testing.T) {
	cancel := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(...any) (any, error) {
			return extension.SessionBeforeCompactResult{Cancel: true}, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{
		settings:      `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`,
		contextWindow: 1_000, maxTokens: 100, extension: cancel,
	}, fauxReply("new answer", ai.StopReasonStop, 0))
	inner := h.session.Inner()
	if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: strings.Repeat("x", 5_000)}}, Timestamp: time.Now().UnixMilli() - 2}}); err != nil {
		t.Fatal(err)
	}
	partial := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "original partial"}}, Provider: "faux", ModelID: "faux-1",
		Usage: &ai.Usage{}, StopReason: ai.StopReasonLength, Timestamp: time.Now().UnixMilli() - 1}
	partialID, err := inner.AppendMessage(agent.AgentMessage{Assistant: partial})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendContextEdit(partialID, &icodingagent.ContextEditReplacement{Content: json.RawMessage(`[{"type":"text","text":"edited partial"}]`)}); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()

	if _, err := h.session.Send(context.Background(), "next prompt"); err != nil {
		t.Fatal(err)
	}
	h.settle(t)

	if omitted, edited := h.omittedTargets(t)[partialID]; !edited || !omitted {
		t.Fatalf("latest edit of %s does not omit it", partialID)
	}
	if projectionContains(h, "edited partial") {
		t.Fatal("projection still contains the edited partial")
	}
}

func TestDurableRecoveryMarksTheExhaustedRetryRunAsFinal(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1}}`},
		fauxError("overloaded_error"),
		fauxError("overloaded_error"),
	)

	_, _ = h.session.Send(context.Background(), "start")
	events := h.settle(t)

	var willRetry []bool
	retryFailed := false
	for _, event := range events {
		switch event := event.(type) {
		case agent.AgentEndEvent:
			willRetry = append(willRetry, event.WillRetry)
		case agent.AutoRetryEndEvent:
			retryFailed = retryFailed || (!event.Success && event.Attempt == 1)
		}
	}
	if len(willRetry) != 2 || !willRetry[0] || willRetry[1] {
		t.Fatalf("agent_end willRetry = %v, want [true false]", willRetry)
	}
	if !retryFailed {
		t.Fatal("auto_retry_end{success:false, attempt:1} missing")
	}
}

func TestDurableRecoveryKeepsOmissionsAndDoesNotRetryWhenRecoveryCompactionFails(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{
		settings:      `{"compaction":{"keepRecentTokens":1,"reserveTokens":0},"retry":{"enabled":false,"maxRetries":0,"baseDelayMs":1}}`,
		contextWindow: 1000, maxTokens: 100,
	},
		fauxReply("partial response", ai.StopReasonLength, 0),
		fauxError("summary failed"),
		fauxReply("must not retry", ai.StopReasonStop, 0),
	)

	if _, err := h.session.Send(context.Background(), strings.Repeat("x", 5000)); err != nil {
		t.Fatal(err)
	}
	h.settle(t)

	if len(h.entries("context_edit")) == 0 {
		t.Fatal("no context_edit omission was persisted")
	}
	if len(h.entries("compaction")) != 0 {
		t.Fatal("a failed recovery compaction appended an entry")
	}
	if ids := messageEntryIDs(t, h, func(m *agent.AssistantMessage) bool { return assistantText(m) == "partial response" }); len(ids) != 1 {
		t.Fatalf("raw partial response entries = %d, want 1", len(ids))
	}
	if projectionContains(h, "partial response") {
		t.Fatal("projection still contains the omitted partial response")
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}

}

func TestRecoveryEventsFollowAgentEndDispatch(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"agent_end": {func(...any) (any, error) {
			once.Do(func() { close(entered); <-release })
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{
		settings: `{"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1}}`, extension: ext,
	}, fauxError("overloaded_error"), fauxError("invalid_api_key"))
	done := make(chan struct{})
	go func() { defer close(done); _, _ = h.session.Send(context.Background(), "start") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("agent_end handler was not entered")
	}
	// Upstream awaits extension handlers before the run continues, so the run
	// waits while agent_end dispatch is held, and every later event follows it.
	select {
	case <-done:
		close(release)
		t.Fatal("the run continued while its agent_end handler was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("provider run did not finish")
	}
	events := h.settle(t)
	ended := 0
	for _, event := range events {
		switch event.(type) {
		case agent.AgentEndEvent:
			ended++
		case agent.AutoRetryStartEvent:
			if ended != 1 {
				t.Fatalf("retry start followed %d agent_end events, want 1", ended)
			}
		case agent.AutoRetryEndEvent:
			if ended != 2 {
				t.Fatalf("retry failure followed %d agent_end events, want 2", ended)
			}
		}
	}
}

func TestQueuedUserMessageResetsRecoveryBudget(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	h.session.overflowRecoveryAttempted.Store(true)
	if err := h.session.persistMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "queued follow-up"}},
	}}); err != nil {
		t.Fatalf("persist queued user message: %v", err)
	}
	if h.session.overflowRecoveryAttempted.Load() {
		t.Fatal("new user message retained the previous recovery budget")
	}
}
