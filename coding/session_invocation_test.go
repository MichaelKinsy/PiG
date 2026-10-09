package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:2006-2035 — direct user sends await the turn, use extension input source and expand only on explicit opt-in.
func TestSendUserMessageInvocationExpansion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expand *bool
		want   string
	}{
		{"default literal", nil, "/greet Alice"},
		{"explicit literal", new(false), "/greet Alice"},
		{"expand template", new(true), "expanded:Alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input extension.InputEvent
			h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) { input = args[0].(extension.InputEvent); return nil, nil }}}}}, fauxReply("done", ai.StopReasonStop, 0))
			h.session.SetPromptResources([]PromptTemplate{{Name: "greet", Content: "expanded:$1"}}, nil)
			if err := h.session.SendUserMessage(t.Context(), "/greet Alice", &extension.SendUserMessageOptions{ExpandPromptTemplates: tc.expand}); err != nil {
				t.Fatal(err)
			}
			if input.Text != "/greet Alice" || input.Source != extension.InputSourceExtension {
				t.Fatalf("input=%+v", input)
			}
			var users []string
			for _, message := range h.session.Messages() {
				if message.User != nil {
					users = append(users, extractUserMessageText(message.User.Content))
				}
			}
			if !reflect.DeepEqual(users, []string{tc.want}) {
				t.Fatalf("user text=%v want=%q", users, tc.want)
			}
			if got := h.provider.callCount(); got != 1 {
				t.Fatalf("provider calls=%d want1", got)
			}
		})
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:1934-1995 — an idle custom append persists before its synchronous message events and does not trigger a model turn by default.
func TestSendCustomMessagePersistsBeforeEvents(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	var events []string
	h.session.Subscribe(func(event agent.AgentEvent) {
		var message agent.AgentMessage
		switch value := event.(type) {
		case agent.MessageStartEvent:
			message = value.Message
			events = append(events, "start")
		case agent.MessageEndEvent:
			message = value.Message
			events = append(events, "end")
		default:
			return
		}
		if message.Custom == nil {
			t.Error("custom message event lost its role")
		}
		found := false
		for _, entry := range h.session.Inner().GetEntries() {
			if entry.Base().Type == "custom_message" {
				found = true
			}
		}
		if !found {
			t.Error("message event preceded custom persistence")
		}
	})
	if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "notice", Content: nil, Display: true}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"start", "end"}) {
		t.Fatalf("events=%v want start,end before return", events)
	}
	if h.provider.callCount() != 0 {
		t.Fatal("idle custom append requested a model turn")
	}
	for _, entry := range h.session.Inner().GetEntries() {
		if entry.Base().Type != "custom_message" {
			continue
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(entry.Raw(), &value); err != nil {
			t.Fatal(err)
		}
		if string(value["content"]) != "[]" {
			t.Fatalf("nil custom content=%s want[]", value["content"])
		}
	}
}

type customMessageWaitTool struct{ notify func(context.Context) error }

func (customMessageWaitTool) Name() string                           { return "wait" }
func (customMessageWaitTool) Label() string                          { return "Wait" }
func (customMessageWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (customMessageWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait for a background task", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool customMessageWaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if err := tool.notify(ctx); err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool done"}}, Details: map[string]any{}}, nil
}

func customMessageOrderingHarness(t *testing.T) *recoveryHarness {
	t.Helper()
	var h *recoveryHarness
	tool := customMessageWaitTool{notify: func(ctx context.Context) error {
		return h.session.SendCustomMessage(ctx, extension.CustomMessageRef{CustomType: "subagent-reply", Content: "subagent replied", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(false)})
	}}
	h = newRecoveryHarness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{tool}}, fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0), fauxReply("second turn", ai.StopReasonStop, 0))
	return h
}

// upstream: packages/coding-agent/test/suite/regressions/8537-custom-message-tool-result-ordering.test.ts:22
func TestCustomMessageAppendsAfterToolResults(t *testing.T) {
	h := customMessageOrderingHarness(t)
	if _, err := h.session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, message := range h.session.Messages() {
		roles = append(roles, message.Role())
	}
	want := []string{"system", "user", "assistant", "toolResult", "custom", "assistant"}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("message roles = %q, want %q", roles, want)
	}
}

// upstream: packages/coding-agent/test/suite/regressions/8537-custom-message-tool-result-ordering.test.ts:61
func TestCustomMessageEntriesAndEventsFollowAgentOrder(t *testing.T) {
	h := customMessageOrderingHarness(t)
	var starts []string
	h.session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.MessageStartEvent); ok {
			starts = append(starts, start.Message.Role())
			if start.Message.Role() == "custom" {
				// A custom start must not precede its entry or the tool result.
				if got := customMessageEntryRoles(h.session); !reflect.DeepEqual(got, []string{"system", "user", "assistant", "toolResult", "custom"}) {
					t.Errorf("entry roles at custom message_start = %q", got)
				}
			}
		}
	})
	if _, err := h.session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	want := []string{"system", "user", "assistant", "toolResult", "custom", "assistant"}
	if got := customMessageEntryRoles(h.session); !reflect.DeepEqual(got, want) {
		t.Errorf("entry roles = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(starts, want) {
		t.Errorf("message_start roles = %q, want %q", starts, want)
	}
}

func customMessageEntryRoles(s *Session) []string {
	var roles []string
	for _, entry := range s.SessionManager().GetBranch() {
		if message, ok := entry.(icodingagent.MessageEntry); ok {
			roles = append(roles, message.Message.Role())
		} else if entry.Base().Type == "custom_message" {
			roles = append(roles, "custom")
		}
	}
	return roles
}

// upstream: packages/coding-agent/test/suite/regressions/8537-custom-message-tool-result-ordering.test.ts:110
func TestCustomMessageLLMHistoryPreservesToolCallResultPair(t *testing.T) {
	h := customMessageOrderingHarness(t)
	for _, prompt := range []string{"hi", "and now?"} {
		if _, err := h.session.Send(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	openToolCallIDs := make(map[string]bool)
	var results int
	for _, message := range agent.ConvertToLLM(agent.NormalizeMessages(h.session.Messages(), nil)) {
		switch message := message.(type) {
		case ai.AssistantMessage:
			clear(openToolCallIDs)
			for _, block := range message.Content {
				if call, ok := block.(ai.ToolCall); ok {
					openToolCallIDs[call.ID] = true
				}
			}
		case ai.ToolResultMessage:
			results++
			if !openToolCallIDs[message.ToolCallID] {
				t.Errorf("tool result %q has no preceding open call", message.ToolCallID)
			}
			delete(openToolCallIDs, message.ToolCallID)
		default:
			clear(openToolCallIDs)
		}
	}
	// The upstream fixture issues exactly one wait call; reject vacuous replay checks.
	if results != 1 {
		t.Fatalf("tool results = %d, want the one wait result", results)
	}
}
