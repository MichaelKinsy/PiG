package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/test/suite/regressions/4167-thinking-toggle-pending-tool-render.test.ts:147,170.
func TestInteractiveRenderedPendingToolsUpstream(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "keeps unresolved rendered tool calls registered for live completion events"
		if historical {
			name = "does not keep completed historical tool calls registered as pending"
		}
		t.Run(name, func(t *testing.T) {
			m, _ := newTickRenderProbe(t, "regular")
			message := &agent.AssistantMessage{
				Role: agent.RoleAssistant, API: "test-api", Provider: "test-provider", ModelID: "test-model",
				Usage: &ai.Usage{}, StopReason: ai.StopReasonToolUse, Timestamp: 1,
				Content: []ai.AssistantContentBlock{ai.ToolCall{
					ID: "tool-4167", Name: "slow_tool", Arguments: map[string]any{"delayMs": 10_000},
				}},
			}
			entries := []SessionEntry{contextFixtureEntry(t, "entry-0", "", "message", map[string]any{"message": message})}
			want := "FINAL_RESULT"
			if historical {
				want = "HISTORICAL_RESULT"
				entries = append(entries, contextFixtureEntry(t, "entry-1", "entry-0", "message", map[string]any{
					"message": &agent.ToolResultMessage{
						Role: agent.RoleToolResult, ToolCallID: "tool-4167", ToolName: "slow_tool", Timestamp: 1,
						Content: []ai.ToolResultMessageContent{ai.TextContent{Text: want}}, IsError: false,
					},
				}))
			}
			m.renderSessionEntryList(entries, false)
			if !historical {
				component := m.toolByID["tool-4167"]
				if component == nil {
					t.Fatal("rendered unresolved tool is not registered for completion")
				}
				m.handleAgentEvent(agent.ToolExecutionEndEvent{
					ToolCallID: "tool-4167", ToolName: "slow_tool",
					Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: want}}, IsError: false},
				})
				if !slices.Contains(m.chatContainer.Children(), tui.Component(component)) {
					t.Fatal("completion replaced the rendered pending component")
				}
			}
			if len(m.toolByID) != 0 {
				t.Fatalf("completed tool calls remain pending: %v", m.toolByID)
			}
			if chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n")); !strings.Contains(chat, want) {
				t.Fatalf("rendered chat lacks %q:\n%s", want, chat)
			}
		})
	}
}

// Pi deletes pendingTools[toolCallId] on tool_execution_end. IDs belong to active
// calls, not the full transcript, so a later call may reuse an earlier ID.
func TestInteractiveReusedToolIDKeepsCompletedCards(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	for _, path := range []string{"first.txt", "second.txt"} {
		args := json.RawMessage(`{"path":"` + path + `"}`)
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "reused", Name: "read"}}}
		m.handleAgentEvent(agent.MessageUpdateEvent{AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: string(args), Partial: partial}})
		m.handleAgentEvent(agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{StopReason: ai.StopReasonToolUse}}})
		m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "reused", ToolName: "read", Args: args})
		m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "reused", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: path}}}})
	}
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	for _, path := range []string{"first.txt", "second.txt"} {
		if count := strings.Count(chat, "read "+path); count != 1 {
			t.Errorf("want one retained card for %s, got %d:\n%s", path, count, chat)
		}
	}
	if len(m.toolByID) != 0 || len(m.toolStarts) != 0 || len(m.toolFileCalls) != 0 {
		t.Errorf("completed calls remain pending: %d identities, %d timers, %d file calls", len(m.toolByID), len(m.toolStarts), len(m.toolFileCalls))
	}
}

func TestInteractivePendingToolLifetime(t *testing.T) {
	for _, boundary := range []struct {
		name  string
		event agent.AgentEvent
	}{
		{"start", agent.AgentStartEvent{}},
		{"end", agent.AgentEndEvent{}},
		{"abort", agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{StopReason: ai.StopReasonAborted}}}},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			m, _ := newTickRenderProbe(t, "regular")
			call := ai.ToolCall{ID: "pending", Name: "read"}
			m.handleAgentEvent(agent.MessageUpdateEvent{AssistantMessageEvent: ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call}})
			card := m.toolByID[call.ID]
			if card == nil {
				t.Fatal("missing streaming card")
			}
			m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: call.ID, ToolName: call.Name, Args: json.RawMessage(`{"path":"pending.txt"}`)})
			m.handleAgentEvent(boundary.event)
			if len(m.toolByID) != 0 || len(m.pendingArgs) != 0 || len(m.toolFileCalls) != 0 {
				t.Fatalf("%s left stale pending state", boundary.name)
			}
			if boundary.name == "abort" && card.State != tui.ToolStateError {
				t.Fatalf("aborted card state=%d", card.State)
			}
		})
	}
}

func TestInteractiveUnmatchedToolEndDoesNotAppendCard(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "unmatched", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "late"}}}})
	if !m.chatContainer.IsEmpty() {
		t.Fatalf("unmatched tool end appended a card: %q", m.chatContainer.Render(100))
	}
}
