package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func projectionViewSession(t *testing.T, exchanges int) *Session {
	t.Helper()
	session := NewSession("view", "/project")
	for i := range exchanges {
		id := fmt.Sprintf("call-%d", i)
		assistant := mkAssistantMsg("reading")
		assistant.Assistant.Content = append(assistant.Assistant.Content, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{"path": "file.go", "offset": float64(i)}})
		result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file text"}}}}
		for _, message := range []agent.AgentMessage{mkUserMsg("go"), assistant, result} {
			if _, err := session.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	return session
}

// pi session-manager.ts buildSessionProjection returns the entries' message objects without copying them. The view does the same for a reader, and projects what BuildSessionProjection projects.
func TestBuildSessionProjectionViewSharesTheEntriesMessages(t *testing.T) {
	session := projectionViewSession(t, 20)
	view := session.BuildSessionProjectionView()
	owned := session.BuildSessionProjection()
	viewJSON, _ := json.Marshal(view.Messages)
	ownedJSON, _ := json.Marshal(owned.Messages)
	if !bytes.Equal(viewJSON, ownedJSON) || len(view.Messages) != 60 {
		t.Fatalf("the view projects %d messages that differ from the projection", len(view.Messages))
	}
	stored := session.GetEntries()[1].(MessageEntry).Message.Assistant
	if view.Messages[1].Assistant != stored {
		t.Fatal("the view copied the stored message")
	}
	if owned.Messages[1].Assistant == stored {
		t.Fatal("BuildSessionProjection returned the stored message to a caller that may modify it")
	}
}

func TestBuildSessionProjectionViewAllocatesLessThanACopyOfEachMessage(t *testing.T) {
	session := projectionViewSession(t, 50)
	session.BuildSessionProjectionView()
	copied := testing.AllocsPerRun(10, func() { session.BuildSessionProjection() })
	viewed := testing.AllocsPerRun(10, func() { session.BuildSessionProjectionView() })
	if viewed*2 > copied {
		t.Fatalf("view %.0f allocations, copy %.0f: the view still copies messages", viewed, copied)
	}
}
