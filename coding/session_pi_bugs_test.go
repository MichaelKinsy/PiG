package coding

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// holdReply blocks the provider call until release closes, so the Session stays streaming while the test mutates its queues.
func holdReply(release <-chan struct{}, text string) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		<-release
		return fauxReply(text, ai.StopReasonStop, 0)(messages)
	}
}

func waitForProviderCalls(t *testing.T, h *recoveryHarness, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.provider.callCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("provider calls=%d, want %d", h.provider.callCount(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Pi #9886: AgentSession.clearQueue() clears agent.followUp/steer queues wholesale, destroying
// custom messages queued by extensions (sendMessage deliverAs followUp/steer) that it does not return.
// PiG (D135) clears and returns only user messages and leaves custom messages queued.
func TestClearQueueKeepsExtensionCustomMessages(t *testing.T) {
	for _, deliverAs := range []extension.DeliverAs{extension.DeliverAsFollowUp, extension.DeliverAsSteer} {
		t.Run(string(deliverAs), func(t *testing.T) {
			release := make(chan struct{})
			h := newRecoveryHarness(t, harnessOptions{}, holdReply(release, "first"), fauxReply("second", ai.StopReasonStop, 0), fauxReply("third", ai.StopReasonStop, 0))
			prompt := make(chan error, 1)
			go func() { prompt <- h.session.Prompt(t.Context(), "start", nil) }()
			waitForProviderCalls(t, h, 1)
			if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "notify", Content: "BACKGROUND-RESULT", Display: true}, &extension.SendMessageOptions{DeliverAs: deliverAs, TriggerTurn: new(true)}); err != nil {
				t.Fatal(err)
			}
			h.session.QueueFollowUp("user queued", nil)
			steering, followUp := h.session.ClearQueue()
			if len(steering) != 0 || !reflect.DeepEqual(followUp, []string{"user queued"}) {
				t.Errorf("ClearQueue = %v, %v", steering, followUp)
			}
			if _, followUps := h.session.Agent().PendingMessages(); len(followUps) != 0 && deliverAs == extension.DeliverAsFollowUp {
				for _, m := range followUps {
					if m.User != nil {
						t.Errorf("user message survived ClearQueue: %+v", m)
					}
				}
			}
			close(release)
			if err := <-prompt; err != nil {
				t.Fatal(err)
			}
			if err := h.session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !projectionContains(h, "BACKGROUND-RESULT") {
				t.Error("the custom message queued by the extension was destroyed by ClearQueue and never delivered")
			}
			if projectionContains(h, "user queued") {
				t.Error("cleared user message was delivered")
			}
		})
	}
}

// Pi #9410: Escape in a session whose context is over the compaction threshold froze the TUI for a blocking summarisation.
// Pi main (agent-session.ts _handlePostAgentRun / _checkCompaction skipAbortedCheck) already skips compaction for an aborted run
// and compacts before the next request, as the maintainers ask; PiG matches, so the report does not reproduce; this test pins that behavior (no fix, no divergence).
// The prior usage (9000) is under the 9900 threshold, so the pre-prompt check does not compact; the long prompt puts the run over it,
// so the post-run check compacts a completed run (the control) and must skip the aborted one. The aborted case fails when the post-run
// loop's abort checks (runPostAgentRunsWith's loop condition and handlePostAgentRun's early returns) are removed.
func TestAbortInOverThresholdSessionDoesNotCompactBeforeSettling(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed run compacts", true: "aborted run does not compact"}[abort], func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`, contextWindow: 10_000, maxTokens: 100}, nil)
			release := make(chan struct{})
			h.provider.responses = []scriptedResponse{holdReply(release, "partial"), fauxReply("summary", ai.StopReasonStop, 0)}
			inner := h.session.Inner()
			for i, text := range []string{"old question", "old answer"} {
				var message agent.AgentMessage
				if i == 0 {
					message = agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli() - 10}}
				} else {
					message = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, Provider: "faux", ModelID: "faux-1",
						Usage: &ai.Usage{Input: 8_995, Output: 5, TotalTokens: 9_000}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli() - 9}}
				}
				if _, err := inner.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			h.session.RefreshContext()
			prompt := make(chan error, 1)
			go func() { prompt <- h.session.Prompt(t.Context(), strings.Repeat("new question ", 400), nil) }()
			waitForProviderCalls(t, h, 1)
			if n := len(h.entries("compaction")); n != 0 {
				t.Fatalf("the pre-prompt check compacted (%d); the fixture must stay under the threshold before the prompt", n)
			}
			abortDone := make(chan error, 1)
			if abort {
				h.session.RequestAbort()
				go func() { abortDone <- h.session.Abort(t.Context()) }()
			} else {
				abortDone <- nil
			}
			close(release)
			<-prompt
			if err := <-abortDone; err != nil {
				t.Fatal(err)
			}
			events := h.settle(t)
			started := 0
			for _, event := range events {
				if _, ok := event.(agent.CompactionStartEvent); ok {
					started++
				}
			}
			want := 1
			if abort {
				want = 0
			}
			if n := len(h.entries("compaction")); n != want || started != want {
				t.Errorf("compaction entries = %d, compaction_start events = %d, want %d", n, started, want)
			}
		})
	}
}

// Pi #10287: the context indicator jumped from 42k to 330k tokens after a retryable error because the retry omission
// (a context_edit after the usage anchor) discarded the anchor. D136 keeps it.
func TestContextUsageSurvivesAnOmittedFailedRetryAttempt(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	inner := h.session.Inner()
	now := time.Now().UnixMilli()
	if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: strings.Repeat("history ", 20_000)}}, Timestamp: now - 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, Provider: "faux", ModelID: "faux-1",
		Usage: &ai.Usage{Input: 4_000, Output: 100, TotalTokens: 4_100}, StopReason: ai.StopReasonStop, Timestamp: now - 4}}); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()
	before := h.session.ContextUsage()
	if before == nil || before.Tokens == nil || *before.Tokens != 4_100 {
		t.Fatalf("usage before = %+v, want 4100", before)
	}
	failedID, err := inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: "WebSocket error", Timestamp: now - 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendContextEdit(failedID, nil); err != nil {
		t.Fatal(err)
	}
	h.session.RefreshContext()
	after := h.session.ContextUsage()
	if after == nil || after.Tokens == nil || *after.Tokens != 4_100 {
		t.Errorf("usage after the omitted failed attempt = %+v, want the anchor 4100 kept", after)
	}
}

// Pi #10162 through the Session: the request the provider receives carries at most inputLimits.images.maxPerRequest images.
func TestPromptSendsAtMostTheModelsImagesPerRequest(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{inputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{MaxPerRequest: 3}}}, fauxReply("ok", ai.StopReasonStop, 0))
	inner := h.session.Inner()
	for i := range 5 {
		if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "screenshot"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}, Timestamp: time.Now().UnixMilli() - int64(100-i)}}); err != nil {
			t.Fatal(err)
		}
	}
	h.session.RefreshContext()
	if err := h.session.Prompt(t.Context(), "continue", nil); err != nil {
		t.Fatal(err)
	}
	h.provider.mu.Lock()
	request := h.provider.requests[0]
	h.provider.mu.Unlock()
	if n := strings.Count(request, `"type":"image"`); n != 3 {
		t.Errorf("request carried %d images, want the model's limit of 3: %s", n, request)
	}
}

// Pi #9051: a session_compact handler publishes passive context with sendMessage({triggerTurn:false}); while the turn is active Pi defers
// that message to the end of the run, so the immediate overflow retry misses it. D138 (Pi PR #9738 flushes the deferred messages once
// the overflow compaction has finished and before the interrupted turn continues).
func TestOverflowRetrySeesCustomMessagesPublishedBySessionCompact(t *testing.T) {
	var h *recoveryHarness
	var retry string
	hooks := summaryOverrideHandlers("summary from extension", nil)
	hooks["session_compact"] = []extension.HandlerFn{func(args ...any) (any, error) {
		if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "restored_context", Content: "RESTORED_CONTEXT_MARKER"}, &extension.SendMessageOptions{TriggerTurn: new(false)}); err != nil {
			t.Error(err)
		}
		return nil, nil
	}}
	h = newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: hooks}, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1}}`},
		fauxReply("one", ai.StopReasonStop, 0),
		fauxError("prompt is too long: 213462 tokens > 200000 maximum"),
		func(messages []ai.Message) *ai.AssistantMessage {
			raw, _ := json.Marshal(messages)
			retry = string(raw)
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		})
	for _, prompt := range []string{"first", "second"} {
		if err := h.session.Prompt(t.Context(), prompt, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if retry == "" {
		t.Fatalf("no overflow retry request; provider calls=%d", h.provider.callCount())
	}
	if !strings.Contains(retry, "RESTORED_CONTEXT_MARKER") {
		t.Errorf("the overflow retry request lacks the session_compact custom message: %s", retry)
	}
}

// Pi #5581, fixed by Pi 1.2.0 (PR #10739): a run that sendMessage({triggerTurn:true}) starts while idle skipped before_agent_start, so the
// first turn used the stock system prompt and ignored the handler's messages. The event fires with messageType "custom" and the run applies
// its result (agent-session.ts:1885-1936 _prepareRun).
func TestIdleCustomMessageRunEmitsBeforeAgentStart(t *testing.T) {
	var events []extension.BeforeAgentStartEvent
	var mu sync.Mutex
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		mu.Lock()
		events = append(events, args[0].(extension.BeforeAgentStartEvent))
		mu.Unlock()
		return &extension.BeforeAgentStartEventResult{
			SystemPrompt: new("CUSTOMIZED-SYSTEM-PROMPT"),
			Message:      &extension.CustomMessageRef{CustomType: "from-handler", Content: "HANDLER-MESSAGE"},
		}, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("done", ai.StopReasonStop, 0))
	if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "wake", Content: "WAKE-UP", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("before_agent_start fired %d times, want 1", len(events))
	}
	if events[0].MessageType != extension.BeforeAgentStartCustom || events[0].Message == nil || events[0].Message.CustomType != "wake" || events[0].Message.Content != "WAKE-UP" {
		t.Errorf("event = %+v", events[0])
	}
	h.provider.mu.Lock()
	request := h.provider.requests[0]
	h.provider.mu.Unlock()
	for _, want := range []string{"CUSTOMIZED-SYSTEM-PROMPT", "HANDLER-MESSAGE", "WAKE-UP"} {
		if !strings.Contains(request, want) {
			t.Errorf("the first request lacks %q: %.400s", want, request)
		}
	}
	if strings.Index(request, "WAKE-UP") > strings.Index(request, "HANDLER-MESSAGE") {
		t.Errorf("the starting message must precede the handler's messages: %.600s", request)
	}
}

// Pi 1.2.0 agent-session.ts:1832 _startRun: the Session is busy from the call of an idle triggerTurn send on, and the caller does not wait for the compaction
// check or the before_agent_start handlers. Messages sent meanwhile join the starting run, and pending nextTurn messages ride with it.
func TestIdleCustomMessageSendReturnsAtOnceAndTheStartingRunIsBusy(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		close(entered)
		<-release
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("done", ai.StopReasonStop, 0))
	if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "queued-first", Content: "NEXT-TURN-CONTEXT"}, &extension.SendMessageOptions{DeliverAs: extension.DeliverAsNextTurn}); err != nil {
		t.Fatal(err)
	}
	returned := make(chan error, 1)
	go func() {
		returned <- h.session.SendMessage(extension.CustomMessageRef{CustomType: "wake", Content: "WAKE-UP", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendMessage blocked on the before_agent_start handler")
	}
	<-entered
	if !h.session.IsStreaming() || h.session.IsIdle() {
		t.Error("the Session must be busy while the starting run's handlers run")
	}
	if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "joined", Content: "JOINED-THE-RUN"}, &extension.SendMessageOptions{TriggerTurn: new(true), DeliverAs: extension.DeliverAsSteer}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := h.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := h.provider.callCount(); n != 1 {
		t.Fatalf("provider calls = %d, want the one starting run", n)
	}
	h.provider.mu.Lock()
	request := h.provider.requests[0]
	h.provider.mu.Unlock()
	for _, want := range []string{"WAKE-UP", "NEXT-TURN-CONTEXT", "JOINED-THE-RUN"} {
		if !strings.Contains(request, want) {
			t.Errorf("the starting run's request lacks %q: %.500s", want, request)
		}
	}
}

// Pi 1.2.0 agent-session.ts:1104-1106: an abort during the start of a custom run does not cancel the handlers. _startRun still starts the run with its messages, and
// _handleAgentEvent aborts it at agent_start, so the custom message is recorded, no request completes and the run settles as aborted.
func TestAbortWhileACustomRunStartsKeepsTheMessageAndSettles(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		close(entered)
		<-release
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("must not run", ai.StopReasonStop, 0))
	if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "wake", Content: "WAKE-UP", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
		t.Fatal(err)
	}
	<-entered
	h.session.RequestAbort()
	close(release)
	if err := h.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	var settled []agent.AgentSettledEvent
	for _, event := range h.events {
		if e, ok := event.(agent.AgentSettledEvent); ok {
			settled = append(settled, e)
		}
	}
	h.mu.Unlock()
	if len(settled) != 1 || !settled[0].Aborted {
		t.Errorf("agent_settled events = %+v, want one aborted", settled)
	}
	if !projectionContains(h, "WAKE-UP") {
		t.Error("the aborted start dropped the custom message")
	}
	if projectionContains(h, "must not run") {
		t.Error("the aborted start completed a request")
	}
	if h.session.IsStreaming() {
		t.Error("the Session is still busy after the aborted start settled")
	}
}

// Pi 1.2.0 agent-session.ts:1857-1876: when agent.prompt finds the Agent held by a run that no start marked busy (here one an SDK caller
// claimed directly), _startRun's finally settles the custom run's start and sendCustomMessage rejects with the Agent's error; the held run
// keeps the Agent.
func TestCustomRunStartThatLosesTheClaimRejectsWithTheAgentError(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		if args[0].(extension.BeforeAgentStartEvent).MessageType == extension.BeforeAgentStartCustom {
			close(entered)
			<-release
		}
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("HELD-ANSWER", ai.StopReasonStop, 0))
	sent := make(chan error, 1)
	go func() {
		sent <- h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "bg", Content: "BACKGROUND-NOTE", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
	}()
	<-entered
	held, err := h.session.Agent().BeginSendContent(t.Context(), BuildUserContent("held", nil))
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-sent; !errors.Is(err, agent.ErrAlreadyProcessingPrompt) {
		t.Fatalf("sendCustomMessage = %v, want the Agent's %q", err, agent.ErrAlreadyProcessingPrompt)
	}
	if !h.session.Agent().IsStreaming() {
		t.Fatal("the rejected start released the held run's Agent")
	}
	if _, err := held.Run(); err != nil {
		t.Fatal(err)
	}
	if !projectionContains(h, "HELD-ANSWER") {
		t.Error("the held run did not complete")
	}
}
