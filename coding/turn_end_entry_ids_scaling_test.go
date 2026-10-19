//go:build !race

package coding

import (
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// turn_end hands extensions the entry id of each tool result of the turn. The event carries copies of the results message_end persisted, so the lookup by message object misses and resolves by tool call id: it must read the newest entries of the projection and not copy the whole history for each result (agent-session.ts _findPersistedMessageEntryId finds the object it persisted).
func TestTurnEndEntryIDsScaleWithTheTurnNotTheHistory(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	const results = 8
	testscaling.Check(t, testscaling.Case{Name: "turn_end entry ids of 8 tool results at history n", Size: 100, Bound: testscaling.Constant, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, 1).session
		persistTurn := func(prefix string) (*agent.AssistantMessage, []agent.ToolResultMessage) {
			calls := make([]ai.AssistantContentBlock, results)
			for i := range calls {
				calls[i] = ai.ToolCall{ID: prefix + strconv.Itoa(i), Name: "read", Arguments: ai.JsonObject{"path": "x.go"}}
			}
			assistant := &agent.AssistantMessage{Role: "assistant", Content: calls, StopReason: ai.StopReasonToolUse, Provider: "scaling", ModelID: "scaling-model", Timestamp: 1}
			if err := s.persistMessage(agent.AgentMessage{Assistant: assistant}); err != nil {
				t.Fatal(err)
			}
			persisted := make([]agent.ToolResultMessage, results)
			for i := range persisted {
				persisted[i] = agent.ToolResultMessage{Role: "toolResult", ToolCallID: prefix + strconv.Itoa(i), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Timestamp: 1}
				if err := s.persistMessage(agent.AgentMessage{ToolResult: &persisted[i]}); err != nil {
					t.Fatal(err)
				}
			}
			return assistant, persisted
		}
		// An earlier turn lets the kept projection grow once, as a running session's does, so the measured turn extends it in place.
		persistTurn("warm_call_")
		s.inner.BuildSessionProjectionView()
		assistant, persisted := persistTurn("turn_call_")
		return func() {
			event := agent.TurnEndEvent{Message: agent.AgentMessage{Assistant: assistant}, ToolResults: append([]agent.ToolResultMessage(nil), persisted...)}
			event = s.turnEndWithEntryIDs(event)
			if event.MessageEntryID == "" {
				panic("turn_end did not resolve the assistant entry")
			}
			for i, id := range event.ToolResultEntryIDs {
				if id == "" {
					panic("turn_end did not resolve tool result " + strconv.Itoa(i))
				}
			}
		}
	}})
}
