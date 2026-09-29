package coding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func boundaryError(message string) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		reply := boundaryReply("", ai.StopReasonError, 0)(messages)
		reply.ErrorMessage = message
		return reply
	}
}

func TestUpstreamSessionBoundariesRecovery(t *testing.T) {
	t.Run("keeps truncated tool attempts in context for the natural next turn", func(t *testing.T) {
		executed := false
		requests := []string{}
		tool := boundaryTool{name: "unsafe_truncated_tool", label: "Unsafe truncated tool", description: "Must not execute from a length response", argument: "value", text: "executed", run: func() { executed = true }}
		h := newBoundaryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}}, boundaryToolReply("unsafe_truncated_tool", ai.JsonObject{"value": "partial"}, ai.StopReasonLength), boundaryCapture(t, &requests, "completed natural continuation"))
		boundaryRecord(t, h, 746, "keeps truncated tool attempts in context for the natural next turn")
		boundaryPrompt(t, h, "start")
		if executed {
			t.Error("truncated tool executed")
		}
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
		if len(requests) == 0 {
			t.Fatal("missing natural continuation request")
		}
		boundaryContains(t, requests[0], "may be truncated", true)
		if n := len(h.entries("context_edit")); n != 0 {
			t.Errorf("context_edit entries=%d", n)
		}
	})
	t.Run("resets length recovery after a successful intermediate assistant turn", func(t *testing.T) {
		tool := boundaryTool{name: "noop", label: "Noop", description: "Noop", text: "done"}
		h := newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"keepRecentTokens":1,"reserveTokens":0}}`, tools: []agent.AgentTool{tool}, extension: summaryFromPreparation("recovered input")}, boundaryReply("first partial", ai.StopReasonLength, 0), boundaryToolReply("noop", ai.JsonObject{}, ai.StopReasonToolUse), boundaryReply("second partial", ai.StopReasonLength, time.Second), boundaryReply("completed second recovery", ai.StopReasonStop, 2*time.Second))
		boundaryRecord(t, h, 779, "resets length recovery after a successful intermediate assistant turn")
		boundaryPrompt(t, h, strings.Repeat("x", 5000))
		boundaryAssertLengthOmissions(t, h)
		if n := h.provider.callCount(); n != 3 {
			t.Errorf("callCount=%d", n)
		}
	})
	t.Run("gives a distinct queued follow-up its own length-recovery budget", func(t *testing.T) {
		queued := false
		var h *recoveryHarness
		ext := summaryFromPreparation("recovered input")
		ext.Handlers["agent_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
			event := args[0].(extension.AgentEndEvent)
			found := false
			for _, message := range event.Messages {
				if message, ok := message.(agent.AgentMessage); ok && message.Assistant != nil && assistantText(message.Assistant) == "first recovered" {
					found = true
				}
			}
			if queued || !found {
				return nil, nil
			}
			queued = true
			return nil, h.session.SendExtensionUserMessage("distinct follow-up", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp})
		}}
		h = newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"keepRecentTokens":1,"reserveTokens":0}}`, extension: ext}, boundaryReply("first partial", ai.StopReasonLength, 0), boundaryReply("first recovered", ai.StopReasonStop, 0), boundaryReply("follow-up partial", ai.StopReasonLength, time.Second), boundaryReply("follow-up recovered", ai.StopReasonStop, 2*time.Second))
		boundaryRecord(t, h, 828, "gives a distinct queued follow-up its own length-recovery budget")
		boundaryPrompt(t, h, strings.Repeat("x", 5000))
		boundaryAssertLengthOmissions(t, h)
		if n := h.provider.callCount(); n != 4 {
			t.Errorf("callCount=%d", n)
		}
	})
	t.Run("finishes retry bookkeeping when a retry receives a nonretryable error", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`}, boundaryError("overloaded_error"), boundaryError("invalid_api_key"))
		boundaryRecord(t, h, 876, "finishes retry bookkeeping when a retry receives a nonretryable error")
		boundaryPrompt(t, h, "start")
		h.settle(t)
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
		found := false
		for _, event := range boundaryEvents[agent.AutoRetryEndEvent](h) {
			if !event.Success && event.Attempt == 1 && event.FinalError == "invalid_api_key" {
				found = true
			}
		}
		if !found {
			t.Errorf("auto_retry_end=%v", boundaryEvents[agent.AutoRetryEndEvent](h))
		}
	})
	t.Run("omits a recoverable projected replacement by its source entry ID", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(...any) (any, error) { return extension.SessionBeforeCompactResult{Cancel: true}, nil }}}}
		h := newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`, extension: ext}, boundaryReply("new answer", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 894, "omits a recoverable projected replacement by its source entry ID")
		if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: BuildUserContent(strings.Repeat("x", 5000), nil), Timestamp: time.Now().UnixMilli() - 2}}); err != nil {
			t.Fatal(err)
		}
		partialID := boundaryAppendAssistant(t, h, "original partial", ai.StopReasonLength, time.Now().UnixMilli()-1, nil)
		boundaryEdit(t, h, partialID, json.RawMessage(`{"content":[{"type":"text","text":"edited partial"}]}`))
		h.session.RefreshContext()
		boundaryPrompt(t, h, "next prompt")
		if omitted, present := h.omittedTargets(t)[partialID]; !present || !omitted {
			t.Errorf("latest edit does not omit %s", partialID)
		}
		if projectionContains(h, "edited partial") {
			t.Error("projection retains edited partial")
		}
	})
	t.Run("recovers an explicit overflow error after a retained boundary replacement", func(t *testing.T) {
		replaced := false
		overflowID := ""
		ext := summaryFromPreparation("recovered overflow")
		ext.Handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
			var fields map[string]any
			if err := json.Unmarshal([]byte(boundaryJSON(t, args[0])), &fields); err != nil {
				t.Error(err)
			}
			if replaced || fields["outcome"] != "error" {
				return nil, nil
			}
			replaced = true
			overflowID = args[0].(extension.TurnEndEvent).MessageEntryID
			return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "context_edit", TargetID: overflowID, Replacement: json.RawMessage(`{"content":[{"type":"text","text":"retained error"}]}`)}), nil
		}}
		overflow := boundaryReply("retained error", ai.StopReasonError, 0)
		h := newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`, extension: ext}, func(messages []ai.Message) *ai.AssistantMessage {
			reply := overflow(messages)
			reply.ErrorMessage = "prompt is too long"
			return reply
		}, boundaryReply("recovered", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 928, "recovers an explicit overflow error after a retained boundary replacement")
		boundaryPrompt(t, h, strings.Repeat("x", 5000))
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
		if overflowID == "" {
			t.Error("overflowId is undefined")
		}
		if omitted, present := h.omittedTargets(t)[overflowID]; !present || !omitted {
			t.Errorf("latest edit does not omit %q", overflowID)
		}
	})
	t.Run("keeps follow-up work behind an automatic error retry", func(t *testing.T) {
		queued := false
		requests, lifecycle := []string{}, []string{}
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
			var fields map[string]any
			if err := json.Unmarshal([]byte(boundaryJSON(t, args[0])), &fields); err != nil {
				t.Error(err)
			}
			if queued || fields["outcome"] != "error" {
				return nil, nil
			}
			queued = true
			return nil, h.session.SendExtensionUserMessage("queued follow-up", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp})
		}}}}
		h = newBoundaryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`, extension: ext}, boundaryError("overloaded_error"), boundaryCapture(t, &requests, "retry recovered"), boundaryCapture(t, &requests, "follow-up completed"))
		boundaryRecord(t, h, 974, "keeps follow-up work behind an automatic error retry")
		h.session.Subscribe(func(event agent.AgentEvent) {
			switch event.(type) {
			case agent.AgentEndEvent:
				lifecycle = append(lifecycle, "agent_end")
			case agent.AutoRetryStartEvent:
				lifecycle = append(lifecycle, "auto_retry_start")
			}
		})
		boundaryPrompt(t, h, "start")
		if n := h.provider.callCount(); n != 3 {
			t.Errorf("callCount=%d", n)
		}
		if len(requests) < 2 {
			t.Errorf("requests=%v", requests)
		} else {
			boundaryContains(t, requests[0], "queued follow-up", false)
			boundaryContains(t, requests[1], "queued follow-up", true)
		}
		if len(lifecycle) < 2 || !reflect.DeepEqual(lifecycle[:2], []string{"agent_end", "auto_retry_start"}) {
			t.Errorf("lifecycle=%v", lifecycle)
		}
	})
	t.Run("marks the exhausted retry run as final", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1}}`}, boundaryError("overloaded_error"), boundaryError("overloaded_error"))
		boundaryRecord(t, h, 1014, "marks the exhausted retry run as final")
		boundaryPrompt(t, h, "start")
		h.settle(t)
		willRetry := []bool{}
		for _, event := range boundaryEvents[agent.AgentEndEvent](h) {
			willRetry = append(willRetry, event.WillRetry)
		}
		if !reflect.DeepEqual(willRetry, []bool{true, false}) {
			t.Errorf("willRetry=%v", willRetry)
		}
		found := false
		for _, event := range boundaryEvents[agent.AutoRetryEndEvent](h) {
			if !event.Success && event.Attempt == 1 {
				found = true
			}
		}
		if !found {
			t.Error("missing failed auto_retry_end attempt 1")
		}
	})
	t.Run("keeps omissions and does not retry when recovery compaction fails", func(t *testing.T) {
		h := newBoundaryHarness(t, harnessOptions{contextWindow: 1000, maxTokens: 100, settings: `{"compaction":{"keepRecentTokens":1,"reserveTokens":0},"retry":{"enabled":false,"maxRetries":0,"baseDelayMs":1}}`}, boundaryReply("partial response", ai.StopReasonLength, 0), func(messages []ai.Message) *ai.AssistantMessage {
			reply := boundaryReply("summary failed", ai.StopReasonError, 0)(messages)
			reply.ErrorMessage = "summary failed"
			return reply
		}, boundaryReply("must not retry", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 1032, "keeps omissions and does not retry when recovery compaction fails")
		boundaryPrompt(t, h, strings.Repeat("x", 5000))
		if n := len(h.entries("context_edit")); n == 0 {
			t.Error("missing context_edit")
		}
		if n := len(h.entries("compaction")); n != 0 {
			t.Errorf("compaction entries=%d", n)
		}
		if ids := messageEntryIDs(t, h, func(message *agent.AssistantMessage) bool { return assistantText(message) == "partial response" }); len(ids) == 0 {
			t.Error("raw partial response was deleted")
		}
		if projectionContains(h, "partial response") {
			t.Error("projection retains partial response")
		}
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
	})
}

func boundaryAssertLengthOmissions(t *testing.T, h *recoveryHarness) {
	t.Helper()
	ids := messageEntryIDs(t, h, func(message *agent.AssistantMessage) bool { return message.StopReason == ai.StopReasonLength })
	if len(ids) != 2 {
		t.Errorf("lengthResponses=%v", ids)
	}
	omitted := h.omittedTargets(t)
	for _, id := range ids {
		if !omitted[id] {
			t.Errorf("length response %s not omitted", id)
		}
	}
}
