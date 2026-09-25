package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

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
		m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "reused", ToolName: "read", Result: agent.AgentToolResult{Content: path}})
	}
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	for _, path := range []string{"first.txt", "second.txt"} {
		if count := strings.Count(chat, "read "+path); count != 1 {
			t.Errorf("want one retained card for %s, got %d:\n%s", path, count, chat)
		}
	}
	if len(m.toolByID) != 0 || len(m.toolStarts) != 0 {
		t.Errorf("completed calls remain pending: %d identities, %d timers", len(m.toolByID), len(m.toolStarts))
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
			m.handleAgentEvent(boundary.event)
			if len(m.toolByID) != 0 || len(m.pendingArgs) != 0 {
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
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "unmatched", ToolName: "read", Result: agent.AgentToolResult{Content: "late"}})
	if !m.chatContainer.IsEmpty() {
		t.Fatalf("unmatched tool end appended a card: %q", m.chatContainer.Render(100))
	}
}
