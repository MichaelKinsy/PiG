package agent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func userAgentMessage(text string) AgentMessage {
	return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}

// upstream: agent.ts:82-107 (createMutableAgentState): initialState seeds tools, messages, model and thinking level; the system prompt seeds a leading system message unless the transcript already starts with one.
func TestAgentInitialStateSeedsState(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	model := scriptedModel(provider)
	seed := []AgentMessage{userAgentMessage("earlier")}
	a := mustNewAgent(AgentOptions{
		InitialState: &AgentInitialState{SystemPrompt: "be brief", Model: model, ThinkingLevel: ai.ThinkingHigh, Tools: []AgentTool{noopTool()}, Messages: seed},
	})
	if a.Model() != model || a.ThinkingLevel() != ai.ThinkingHigh || len(a.Tools()) != 1 {
		t.Fatalf("model=%v thinking=%q tools=%d", a.Model() == model, a.ThinkingLevel(), len(a.Tools()))
	}
	messages := a.Messages()
	if len(messages) != 2 || messages[0].System == nil || messages[1].User == nil {
		t.Fatalf("transcript = %+v, want the system prompt message then the seeded message", messages)
	}
	// The seed is copied, not aliased.
	seed[0] = userAgentMessage("mutated")
	if got := a.Messages()[1].User.Content.(ai.UserContentBlocks); len(got) != 1 || got[0].(ai.TextContent).Text != "earlier" {
		t.Fatalf("seed was aliased: %+v", got)
	}
}

func TestAgentInitialStateKeepsALeadingSystemMessage(t *testing.T) {
	system := AgentMessage{System: ai.CreateInitialSystemMessage("from transcript", nil)}
	a := mustNewAgent(AgentOptions{InitialState: &AgentInitialState{SystemPrompt: "ignored", Messages: []AgentMessage{system, userAgentMessage("hi")}}})
	messages := a.Messages()
	if len(messages) != 2 || messages[0].System == nil || messages[1].User == nil {
		t.Fatalf("transcript = %+v, want no second system message", messages)
	}
}

func TestAgentInitialStateLeavesFlattenedOptionsAlone(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	model := scriptedModel(provider)
	a := mustNewAgent(AgentOptions{Model: model, SystemPrompt: "flat", InitialState: &AgentInitialState{}})
	if a.Model() != model || a.ThinkingLevel() != ai.ThinkingOff {
		t.Fatalf("an empty initial state must not override the flattened options: %v %q", a.Model() == model, a.ThinkingLevel())
	}
	if messages := a.Messages(); len(messages) != 1 || messages[0].System == nil {
		t.Fatalf("transcript = %+v, want the flat system prompt", messages)
	}
}
