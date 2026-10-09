package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestUpstreamSessionBoundariesAccounting(t *testing.T) {
	for _, omitAssistant := range []bool{true, false} {
		site, name, window, reserve, input, prompt, reply := 541, "does not trigger successful-response overflow from usage captured before a boundary edit", 5000, 0, 5100, "large input that is later omitted", "done"
		if omitAssistant {
			site, name, window, reserve, input, prompt, reply = 506, "does not compact from usage belonging to a boundary-omitted assistant", 10000, 300, 9800, "small prompt", "short response"
		}
		t.Run(name, func(t *testing.T) {
			handled := false
			var h *recoveryHarness
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
				"message_end": {func(args ...any) (any, error) {
					event := args[0].(extension.MessageEndEvent)
					message := event.Message
					if message.Assistant == nil {
						return nil, nil
					}
					replacement := *message.Assistant
					usage := *replacement.Usage
					usage.Input = input
					usage.Output = 1
					usage.TotalTokens = input + 1
					replacement.Usage = &usage
					result := extension.AgentMessage(agent.AgentMessage{Assistant: &replacement})
					return &extension.MessageEndEventResult{Message: &result}, nil
				}},
				"turn_end": {func(args ...any) (any, error) {
					if handled {
						return nil, nil
					}
					handled = true
					target := args[0].(extension.TurnEndEvent).MessageEntryID
					if !omitAssistant {
						target = boundaryLastUser(t, h)
					}
					return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "context_edit", TargetID: target}), nil
				}},
			}}
			h = newBoundaryHarness(t, harnessOptions{extension: ext, contextWindow: window, maxTokens: 100, settings: boundaryJSON(t, map[string]any{"compaction": map[string]any{"enabled": true, "keepRecentTokens": 1, "reserveTokens": reserve}})}, boundaryReply(reply, ai.StopReasonStop, 0))
			boundaryRecord(t, h, site, name)
			boundaryPrompt(t, h, prompt)
			h.settle(t)
			if events := boundaryEvents[agent.CompactionStartEvent](h); len(events) != 0 {
				t.Errorf("compaction_start=%v", events)
			}
			usage := h.session.ContextUsage()
			if usage == nil || usage.Tokens == nil || *usage.Tokens >= 2000 {
				t.Errorf("usage=%+v, want tokens <2000", usage)
			}
		})
	}
	t.Run("does not trigger threshold compaction from post-edit usage captured before a later compaction", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{contextWindow: 10000, maxTokens: 100, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`})
		boundaryRecord(t, h, 578, "does not trigger threshold compaction from post-edit usage captured before a later compaction")
		userID, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: BuildUserContent("small input", nil), Timestamp: time.Now().UnixMilli() - 3}})
		if err != nil {
			t.Fatal(err)
		}
		boundaryEdit(t, h, userID, json.RawMessage(`{"content":"edited input"}`))
		boundaryAppendAssistant(t, h, "answer", ai.StopReasonStop, time.Now().UnixMilli()-2, &ai.Usage{Input: 50000, Output: 1, TotalTokens: 50001})
		if _, err := h.session.Inner().AppendCompaction("small summary", userID, 50001, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		h.session.RefreshContext()
		errorMessage := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}, Provider: "faux", ModelID: "faux-1", Usage: &ai.Usage{}, StopReason: ai.StopReasonError, ErrorMessage: "invalid_api_key", Timestamp: time.Now().UnixMilli() + 1000}
		calls := 0
		continued, err := h.session.checkCompactionDecision(t.Context(), errorMessage, true, nil, func(ctx context.Context, reason string, willRetry bool) (bool, error) {
			calls++
			return h.session.autoCompactAndDecide(ctx, reason, willRetry)
		})
		if err != nil || continued {
			t.Fatalf("checkCompaction=%v,%v", continued, err)
		}
		if calls != 0 {
			t.Errorf("runAutoCompaction called %d times, want no calls", calls)
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Pi spies on method entry, including calls that return before publishing an event. Keep event and persistence checks as additional guards.
		if n := len(boundaryEvents[agent.CompactionStartEvent](h)); n != 0 {
			t.Errorf("runAutoCompaction started %d times", n)
		}
		if n := len(h.entries("compaction")); n != 1 {
			t.Errorf("compaction entries=%d", n)
		}
	})
	t.Run("does not treat retained pre-compaction assistant usage as post-compaction usage", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{})
		boundaryRecord(t, h, 617, "does not treat retained pre-compaction assistant usage as post-compaction usage")
		id := boundaryAppendAssistant(t, h, "retained", ai.StopReasonStop, time.Now().UnixMilli(), &ai.Usage{Input: 10000, TotalTokens: 10001})
		if _, err := h.session.Inner().AppendCompaction("summary", id, 10001, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		h.session.RefreshContext()
		if usage := h.session.ContextUsage(); usage == nil || usage.Tokens != nil {
			t.Errorf("context usage=%+v, want null tokens", usage)
		}
	})
	t.Run("persists custom context sent during pre-settlement before continuing", func(t *testing.T) {
		handled := false
		requests := []string{}
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
			if handled {
				return nil, nil
			}
			handled = true
			if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "pending-boundary", Content: "persist before continue", Display: false}, &extension.SendMessageOptions{TriggerTurn: new(false)}); err != nil {
				t.Error(err)
			}
			return extension.BoundaryResult{Continue: new(true)}, nil
		}}}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"))
		boundaryRecord(t, h, 629, "persists custom context sent during pre-settlement before continuing")
		boundaryPrompt(t, h, "start")
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
		if len(requests) == 0 {
			t.Fatal("missing continuation request")
		}
		boundaryContains(t, requests[0], "persist before continue", true)
		boundaryEntry(t, h, "custom_message", "pending-boundary")
	})
	t.Run("does not consume queued input when pre-settlement drafts leave system-only context", func(t *testing.T) {
		handled := false
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
			if handled {
				return nil, nil
			}
			handled = true
			if err := h.session.SendExtensionUserMessage("still queued", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
				t.Error(err)
			}
			entries := []extension.SessionBoundaryDraft{}
			for _, entry := range h.session.Inner().GetBranch() {
				if message, ok := entry.(icodingagent.MessageEntry); ok && (message.Message.User != nil || message.Message.Assistant != nil || message.Message.ToolResult != nil) {
					entries = append(entries, extension.SessionBoundaryDraft{Type: "context_edit", TargetID: entry.Base().ID})
				}
			}
			return boundaryDrafts(true, entries...), nil
		}}}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("must not run", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 665, "does not consume queued input when pre-settlement drafts leave system-only context")
		boundaryPrompt(t, h, "start")
		if n := h.provider.callCount(); n != 1 {
			t.Errorf("callCount=%d", n)
		}
		if n := h.session.PendingMessageCount(); n != 1 {
			t.Errorf("pendingMessageCount=%d", n)
		}
	})
	t.Run("commits pre-settlement drafts but suppresses continuation when aborted during the hook", func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
			close(started)
			<-release
			return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "custom", CustomType: "committed-after-abort", Data: true}), nil
		}}}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("must not run", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 705, "commits pre-settlement drafts but suppresses continuation when aborted during the hook")
		prompt := make(chan error, 1)
		go func() { err := h.session.Prompt(t.Context(), "start", nil); prompt <- err }()
		<-started
		// JS abort executes its cancellation prologue before returning a Promise. RequestAbort performs that same synchronous prologue; Abort joins it concurrently with prompt completion.
		h.session.RequestAbort()
		abort := make(chan error, 1)
		go func() { abort <- h.session.Abort(t.Context()) }()
		close(release)
		if err := <-prompt; err != nil {
			t.Error(err)
		}
		if err := <-abort; err != nil {
			t.Error(err)
		}
		if n := h.provider.callCount(); n != 1 {
			t.Errorf("callCount=%d", n)
		}
		if entry := boundaryEntry(t, h, "custom", "committed-after-abort"); entry["data"] != true {
			t.Errorf("entry=%v", entry)
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		h.settle(t)
		// agent-session-boundaries.test.ts:735-736 (1.1.0, #10607): settlement reports the abort although the last response succeeded.
		if got := boundaryEvents[agent.AgentSettledEvent](h); !reflect.DeepEqual(got, []agent.AgentSettledEvent{{Aborted: true}}) {
			t.Errorf("agent_settled=%+v", got)
		}
	})
}
