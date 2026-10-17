package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The refresh projects the same context as an owned projection of the session, for every entry kind that reaches the model, and a
// compaction or context edit written between two refreshes reaches the next one.
func TestNextTurnRefreshProjectsWhatTheSessionProjects(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":false}}`, contextWindow: 1_000_000, maxTokens: 100}, nil)
	inner := h.session.Inner()
	stamp := time.Now().UnixMilli() - 100_000
	check := func(step string) {
		t.Helper()
		update, err := h.session.prepareNextTurn(t.Context(), agent.PrepareNextTurnContext{})
		if err != nil || update == nil || update.Context == nil {
			t.Fatalf("%s: prepareNextTurn = %v, %v", step, update, err)
		}
		got, err := json.Marshal(update.Context.Messages)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(inner.BuildSessionProjection().Messages)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: the refresh projected\n%s\nthe session projects\n%s", step, got, want)
		}
	}
	append3 := func(i int) {
		call := fmt.Sprintf("call-%d", i)
		user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: stamp}}
		assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reading"}, ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": "a.txt"}}}, Provider: "faux", ModelID: "faux-1",
			Usage: &ai.Usage{Input: 95, Output: 5, TotalTokens: 100}, StopReason: ai.StopReasonToolUse, Timestamp: stamp + 1}}
		result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file text"}}, Timestamp: stamp + 2}}
		for _, message := range []agent.AgentMessage{user, assistant, result} {
			if _, err := inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
		stamp += 3
	}
	check("empty session")
	for i := range 3 {
		append3(i)
		check(fmt.Sprintf("after exchange %d", i))
	}
	var target string
	for _, entry := range inner.GetEntries() {
		if entry.Base().Type == "message" {
			target = entry.Base().ID
			break
		}
	}
	if _, err := inner.AppendContextEdit(target, nil); err != nil {
		t.Fatal(err)
	}
	check("after a context edit")
	append3(3)
	check("after an exchange following the edit")
}

// A hook the supplied Agent had before the Session installed its own receives the refreshed context and may write the array it is handed. The
// projection the context is made from is the session's own, so that write must land in a copy of the array: the next refresh still projects the
// session whole.
func TestNextTurnRefreshHookWritingItsMessageArrayDoesNotReachTheSession(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":false}}`, contextWindow: 1_000_000, maxTokens: 100}, nil)
	inner := h.session.Inner()
	stamp := time.Now().UnixMilli() - 100_000
	for i := range 3 {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: stamp}},
			{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, Provider: "faux", ModelID: "faux-1",
				Usage: &ai.Usage{Input: 95, Output: 5, TotalTokens: 100}, StopReason: ai.StopReasonStop, Timestamp: stamp + 1}},
		} {
			if _, err := inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
		stamp += 2
	}
	want, err := json.Marshal(inner.BuildSessionProjection().Messages)
	if err != nil {
		t.Fatal(err)
	}
	h.session.previousPrepareNextTurn = func(_ context.Context, turn agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
		for i := range turn.Context.Messages {
			turn.Context.Messages[i] = agent.AgentMessage{}
		}
		return nil, nil
	}
	if _, err := h.session.prepareNextTurn(t.Context(), agent.PrepareNextTurnContext{}); err != nil {
		t.Fatal(err)
	}
	h.session.previousPrepareNextTurn = nil
	update, err := h.session.prepareNextTurn(t.Context(), agent.PrepareNextTurnContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(update.Context.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("a hook's writes to its message array reached the session: refresh projected\n%s\nthe session projects\n%s", got, want)
	}
}
