package subprocess

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func agentUserMessage(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text), Timestamp: 1}}
}

func agentUserText(t *testing.T, message agent.AgentMessage) string {
	t.Helper()
	text, err := json.Marshal(message.User.Content)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// agent_end hands every handler the one list of messages (runner.ts emit) and the session keeps those objects, so a handler's edit of one of them, by its index, is the message the event holds afterwards.
func TestAgentEndReplyEditsTheMessageAtItsIndex(t *testing.T) {
	event := extension.AgentEndEvent{Type: "agent_end", Messages: []agent.AgentMessage{agentUserMessage("first"), agentUserMessage("second")}}
	raw := `{"_pigEventEdits":[{"at":1,"message":{"role":"user","content":"changed","timestamp":1}}],"_pigEventResult":null}`
	if _, err := unwrapEventReply("agent_end", event, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	if got := agentUserText(t, event.Messages[0]); got != `"first"` {
		t.Errorf("message 0 holds %s, want it untouched", got)
	}
	if got := agentUserText(t, event.Messages[1]); got != `"changed"` {
		t.Errorf("message 1 holds %s, want the edit", got)
	}
}

// An edit that does not fit refuses the whole reply, and none of its messages is applied.
func TestAgentEndReplyRefusesAMisfitAndAppliesNone(t *testing.T) {
	good := `{"at":0,"message":{"role":"user","content":"changed","timestamp":1}}`
	for name, edits := range map[string]string{
		"past the list": `[` + good + `,{"at":2,"message":{"role":"user","content":"x","timestamp":1}}]`,
		"negative":      `[` + good + `,{"at":-1,"message":{"role":"user","content":"x","timestamp":1}}]`,
		"another role":  `[` + good + `,{"at":1,"message":{"role":"assistant","content":[],"timestamp":1}}]`,
		"named twice":   `[` + good + `,` + good + `]`,
	} {
		event := extension.AgentEndEvent{Type: "agent_end", Messages: []agent.AgentMessage{agentUserMessage("first"), agentUserMessage("second")}}
		if _, err := unwrapEventReply("agent_end", event, json.RawMessage(`{"_pigEventEdits":`+edits+`,"_pigEventResult":null}`)); err == nil {
			t.Errorf("%s: the reply was not refused", name)
		}
		if got := agentUserText(t, event.Messages[0]); strings.Contains(got, "changed") {
			t.Errorf("%s: message 0 holds %s after a refused reply", name, got)
		}
	}
}
